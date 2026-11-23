;; Compiler-side driver for asm/indexbyte.wat.
(module
  (type $go.object (sub (struct)))
  (type $go.bytes (array (mut i8)))
  (type $go.string (sub $go.object (struct
    (field $data (ref $go.bytes))
    (field $off  i32)
    (field $len  i32))))

  (import "goasm" "indexbyte"
    (func $indexbyte (param (ref $go.string) i32) (result i32)))

  (func $panic_bounds (export "panic_bounds")
    (unreachable)
  )

  (func $make_hell (result (ref $go.string))
    (local $arr (ref $go.bytes))
    (local.set $arr (array.new_default $go.bytes (i32.const 4)))
    (array.set $go.bytes (local.get $arr) (i32.const 0) (i32.const 104)) ;; h
    (array.set $go.bytes (local.get $arr) (i32.const 1) (i32.const 101)) ;; e
    (array.set $go.bytes (local.get $arr) (i32.const 2) (i32.const 108)) ;; l
    (array.set $go.bytes (local.get $arr) (i32.const 3) (i32.const 108)) ;; l
    (struct.new $go.string (local.get $arr) (i32.const 0) (i32.const 4))
  )

  (func (export "find_e") (result i32)
    (call $indexbyte (call $make_hell) (i32.const 101))
  )

  (func (export "find_missing") (result i32)
    (call $indexbyte (call $make_hell) (i32.const 120))
  )
)
