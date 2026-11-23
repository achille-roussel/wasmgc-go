;; Panic / recover shape from docs/design/04-runtime-and-abi.md.
;;
;; $go.panic carries a GC object, not a bare integer. A frame with no
;; try_table does not catch it (the exception passes through). A frame
;; that has defers catches with catch_ref, runs the defer list, and
;; either swallows the exception or rethrows the same exnref with
;; throw_ref. Assembled with `wasm-tools parse` (wabt 1.0.42's
;; wat2wasm does not recognize struct.get).
(module
  (type $panic (struct (field $value i32) (field $recovered (mut i32))))
  (tag $go.panic (param (ref $panic)))

  (global $deferRuns (mut i32) (i32.const 0))

  (func $boom (param $v i32)
    (throw $go.panic
      (struct.new $panic
        (local.get $v)
        (i32.const 0)))
  )

  ;; No handler. The exception propagates to the caller.
  (func $passthrough (param $v i32)
    (call $boom (local.get $v))
  )

  (func $noteDefer
    (global.set $deferRuns
      (i32.add (global.get $deferRuns) (i32.const 1)))
  )

  ;; Catch, run one defer, swallow if the payload value is 7.
  ;; Returns value+1 on the swallow path so the caller can see both
  ;; the payload and that recovered was stored on the object.
  (func (export "recover7") (result i32)
    (local $p (ref $panic))
    (local $ex exnref)
    (block $return (result i32)
      (block $handler (result (ref $panic) exnref)
        (try_table (catch_ref $go.panic $handler)
          (call $passthrough (i32.const 7))
          (i32.const 0)
          (br $return)
        )
        unreachable
      )
      (local.set $ex)
      (local.set $p)
      (call $noteDefer)
      (if (i32.eq (struct.get $panic $value (local.get $p)) (i32.const 7))
        (then
          (struct.set $panic $recovered (local.get $p) (i32.const 1))
          (drop (local.get $ex))
          (br $return
            (i32.add
              (struct.get $panic $value (local.get $p))
              (struct.get $panic $recovered (local.get $p))))
        )
      )
      (throw_ref (local.get $ex))
      unreachable
    )
  )

  ;; Inner frame runs its defer and rethrows. Outer frame must observe
  ;; the same value (8) and its own defer must also run.
  (func $rethrowInner
    (local $p (ref $panic))
    (local $ex exnref)
    (block $handler (result (ref $panic) exnref)
      (try_table (catch_ref $go.panic $handler)
        (call $passthrough (i32.const 8))
        return
      )
      unreachable
    )
    (local.set $ex)
    (local.set $p)
    (call $noteDefer)
    (throw_ref (local.get $ex))
    unreachable
  )

  (func (export "propagate") (result i32)
    (local $p (ref $panic))
    (local $ex exnref)
    (block $return (result i32)
      (block $handler (result (ref $panic) exnref)
        (try_table (catch_ref $go.panic $handler)
          (call $rethrowInner)
          (i32.const 0)
          (br $return)
        )
        unreachable
      )
      (local.set $ex)
      (local.set $p)
      (call $noteDefer)
      (drop (local.get $ex))
      (br $return (struct.get $panic $value (local.get $p)))
    )
  )

  ;; Normal return still runs the defer. The catch clause targets a
  ;; block whose result type is empty, because catch_all pushes nothing.
  (func (export "normalDefer") (result i32)
    (block $return (result i32)
      (block $ignored
        (try_table (catch_all $ignored)
          (call $noteDefer)
          (i32.const 1)
          (br $return)
        )
        unreachable
      )
      (i32.const -1)
    )
  )

  (func (export "deferRuns") (result i32)
    (global.get $deferRuns)
  )

  (func (export "reset")
    (global.set $deferRuns (i32.const 0))
  )
)
