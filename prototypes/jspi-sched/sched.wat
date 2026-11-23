;; JSPI scheduling assumptions from docs/design/04-runtime-and-abi.md
;; and docs/design/07-host-and-js.md.
;;
;; park is a suspending import. ready is not. Locals, including a
;; (ref $box), survive a suspend. The mutable global $g is shared by
;; every activation; the parking function restores it from a local.
;; spawn is a non-suspending import the host turns into a macrotask
;; that enters child under its own WebAssembly.promising wrapper.
(module
  (type $box (struct (field i32)))

  (import "host" "park" (func $park (param i32) (result i32)))
  (import "host" "ready" (func $ready (param i32)))
  (import "host" "spawn" (func $spawn))
  (import "host" "log" (func $log (param i32)))
  (import "host" "makeBox" (func $makeBox (result (ref $box))))
  (import "host" "susExt" (func $susExt (result externref)))
  (import "host" "idExt" (func $idExt (param externref) (result externref)))

  (global $g (mut i32) (i32.const 0))

  (func (export "peek") (result i32)
    (global.get $g)
  )

  ;; Save $g, ask the host to run child later, park, restore $g.
  (func (export "parent") (result i32)
    (local $saved i32)
    (global.set $g (i32.const 1))
    (local.set $saved (global.get $g))
    (call $spawn)
    (drop (call $park (i32.const 1)))
    (global.set $g (local.get $saved))
    (global.get $g)
  )

  ;; Runs on a second promising activation while parent is suspended.
  ;; Clobbers $g, logs the clobber, then readies parent.
  (func (export "child")
    (global.set $g (i32.const 99))
    (call $log (global.get $g))
    (call $ready (i32.const 1))
  )

  ;; Schedules a macrotask and returns. Does not park.
  (func (export "exit") (result i32)
    (call $spawn)
    (i32.const 1)
  )

  ;; ready-before-park: host returns a number, not a Promise.
  (func (export "early") (result i32)
    (call $ready (i32.const 5))
    (call $park (i32.const 5))
  )

  (func (export "local") (result i32)
    (local $x i32)
    (local.set $x (i32.const 42))
    (drop (call $park (i32.const 3)))
    (local.get $x)
  )

  (func (export "boxy") (result i32)
    (local $b (ref $box))
    (local.set $b (struct.new $box (i32.const 7)))
    (drop (call $park (i32.const 2)))
    (struct.get $box 0 (local.get $b))
  )

  ;; Two suspends. The local is updated across them.
  (func (export "twice") (result i32)
    (local $x i32)
    (local.set $x (i32.const 1))
    (drop (call $park (i32.const 4)))
    (local.set $x (i32.add (local.get $x) (i32.const 10)))
    (drop (call $park (i32.const 4)))
    (local.get $x)
  )

  (func (export "reject")
    (drop (call $park (i32.const 9)))
  )

  (func (export "newBox") (result (ref $box))
    (struct.new $box (i32.const 5))
  )

  (func (export "fromHost") (result i32)
    (struct.get $box 0 (call $makeBox))
  )

  (func (export "susExt") (result externref)
    (call $susExt)
  )

  (func (export "idExt") (param externref) (result externref)
    (call $idExt (local.get 0))
  )
)
