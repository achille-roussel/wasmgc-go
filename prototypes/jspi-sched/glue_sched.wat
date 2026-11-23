;; Integration with lib/wasmgc.js: go.runtime.park/ready/spawn and export _sched.
;; Macrotask spawn vs microtask main-exit; parked main runs the child.
;; Parse: wasm-tools parse glue_sched.wat -o glue_sched.wasm
(module
  (import "go" "runtime.park" (func $park (param i32) (result i32)))
  (import "go" "runtime.ready" (func $ready (param i32)))
  (import "go" "runtime.spawn" (func $spawn))

  (global $g (mut i32) (i32.const 0))
  (global $childRan (mut i32) (i32.const 0))
  (global $mode (mut i32) (i32.const 0)) ;; 0=exit-without-park, 1=park-then-child

  ;; run: mode 0 spawns and returns (child must NOT run before process exit).
  ;;      mode 1 spawns, parks; _sched child readies us.
  (func (export "run")
    (local $saved i32)
    (global.set $childRan (i32.const 0))
    (global.set $g (i32.const 1))
    (if (i32.eqz (global.get $mode))
      (then
        (call $spawn)
        ;; return — glue microtask exits before spawn macrotask
      )
      (else
        (local.set $saved (global.get $g))
        (call $spawn)
        (drop (call $park (i32.const 1)))
        (global.set $g (local.get $saved))
      )
    )
  )

  (func (export "_sched")
    (global.set $g (i32.const 99))
    (global.set $childRan (i32.const 1))
    (call $ready (i32.const 1))
  )

  (func (export "set_mode") (param i32)
    (global.set $mode (local.get 0)))

  (func (export "child_ran") (result i32)
    (global.get $childRan))

  (func (export "peek_g") (result i32)
    (global.get $g))
)
