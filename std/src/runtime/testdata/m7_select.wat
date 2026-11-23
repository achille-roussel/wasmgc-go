;; M7 select park (docs/design/04): enqueue waiters on two chans, park once,
;; partner completes case 1 — no 1 ms poll.
(module
  (import "go" "runtime.park" (func $park (param i32) (result i32)))
  (import "go" "runtime.ready" (func $ready (param i32)))
  (import "go" "runtime.spawn" (func $spawn))

  (type $go.object (sub (struct)))
  (rec
    (type $waiter (sub $go.object (struct
      (field $park (mut i32))
      (field $casi i32)
      (field $elem (mut i32))
      (field $has  (mut i32))
      (field $next (mut (ref null $waiter))))))

    (type $hchan (sub $go.object (struct
      (field $recvHead (mut (ref null $waiter)))
      (field $recvTail (mut (ref null $waiter))))))
  )

  (global $selDone (mut i32) (i32.const 0))
  (global $selWinner (mut i32) (i32.const -1))
  (global $c0 (mut (ref null $hchan)) (ref.null $hchan))
  (global $c1 (mut (ref null $hchan)) (ref.null $hchan))
  (global $job (mut i32) (i32.const 0))

  (func $makechan (result (ref $hchan))
    (struct.new $hchan (ref.null $waiter) (ref.null $waiter)))

  (func $enq (param $c (ref $hchan)) (param $w (ref $waiter))
    (local $t (ref null $waiter))
    (local.set $t (struct.get $hchan $recvTail (local.get $c)))
    (if (ref.is_null (local.get $t))
      (then
        (struct.set $hchan $recvHead (local.get $c) (local.get $w))
        (struct.set $hchan $recvTail (local.get $c) (local.get $w)))
      (else
        (struct.set $waiter $next (ref.as_non_null (local.get $t)) (local.get $w))
        (struct.set $hchan $recvTail (local.get $c) (local.get $w)))))

  (func $deq (param $c (ref $hchan)) (result (ref null $waiter))
    (local $w (ref null $waiter))
    (local.set $w (struct.get $hchan $recvHead (local.get $c)))
    (if (ref.is_null (local.get $w)) (then (return (ref.null $waiter))))
    (struct.set $hchan $recvHead (local.get $c)
      (struct.get $waiter $next (ref.as_non_null (local.get $w))))
    (if (ref.is_null (struct.get $hchan $recvHead (local.get $c)))
      (then (struct.set $hchan $recvTail (local.get $c) (ref.null $waiter))))
    (local.get $w))

  (func $complete (param $c (ref $hchan)) (param $v i32) (result i32)
    (local $w (ref null $waiter))
    (local $ww (ref $waiter))
    (local.set $w (call $deq (local.get $c)))
    (if (ref.is_null (local.get $w)) (then (return (i32.const 0))))
    (local.set $ww (ref.as_non_null (local.get $w)))
    (if (global.get $selDone) (then (return (i32.const 0))))
    (global.set $selDone (i32.const 1))
    (global.set $selWinner (struct.get $waiter $casi (local.get $ww)))
    (struct.set $waiter $elem (local.get $ww) (local.get $v))
    (struct.set $waiter $has (local.get $ww) (i32.const 1))
    (call $ready (struct.get $waiter $park (local.get $ww)))
    (i32.const 1))

  (func (export "run") (nop))

  (func (export "_sched")
    (if (i32.eq (global.get $job) (i32.const 1))
      (then
        (drop (call $complete (ref.as_non_null (global.get $c1)) (i32.const 99)))
        (global.set $job (i32.const 0)))))

  ;; Returns (winner<<8)|value — expect (1<<8)|99 = 355
  (func (export "select_park") (result i32)
    (local $w0 (ref $waiter))
    (local $w1 (ref $waiter))
    (local $id i32)
    (global.set $c0 (call $makechan))
    (global.set $c1 (call $makechan))
    (global.set $selDone (i32.const 0))
    (global.set $selWinner (i32.const -1))
    (local.set $id (i32.const 50))
    (local.set $w0
      (struct.new $waiter
        (local.get $id) (i32.const 0) (i32.const 0) (i32.const 0) (ref.null $waiter)))
    (local.set $w1
      (struct.new $waiter
        (local.get $id) (i32.const 1) (i32.const 0) (i32.const 0) (ref.null $waiter)))
    (call $enq (ref.as_non_null (global.get $c0)) (local.get $w0))
    (call $enq (ref.as_non_null (global.get $c1)) (local.get $w1))
    (global.set $job (i32.const 1))
    (call $spawn)
    (drop (call $park (local.get $id)))
    (i32.add
      (i32.shl (global.get $selWinner) (i32.const 8))
      (struct.get $waiter $elem (local.get $w1))))
)
