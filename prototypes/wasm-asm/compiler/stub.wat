;; Stand-in for compiler output: prelude types from
;; docs/design/03-type-mapping.md, plus a tiny driver that imports
;; goasm.memequal the way a body-less Go call would lower.
(module
  (type $go.object (sub (struct)))
  (type $go.bytes (array (mut i8)))
  (type $go.string (sub $go.object (struct
    (field $data (ref $go.bytes))
    (field $off  i32)
    (field $len  i32))))

  (import "goasm" "memequal"
    (func $memequal (param (ref $go.string) (ref $go.string)) (result i32)))

  ;; Build a string header over three bytes.
  (func $make3 (param $a i32) (param $b i32) (param $c i32) (result (ref $go.string))
    (local $arr (ref $go.bytes))
    (local.set $arr (array.new_default $go.bytes (i32.const 3)))
    (array.set $go.bytes (local.get $arr) (i32.const 0) (local.get $a))
    (array.set $go.bytes (local.get $arr) (i32.const 1) (local.get $b))
    (array.set $go.bytes (local.get $arr) (i32.const 2) (local.get $c))
    (struct.new $go.string (local.get $arr) (i32.const 0) (i32.const 3))
  )

  ;; Same backing, substring headers with different off/len.
  (func $make_sub (result (ref $go.string) (ref $go.string))
    (local $arr (ref $go.bytes))
    (local.set $arr (array.new_default $go.bytes (i32.const 5)))
    (array.set $go.bytes (local.get $arr) (i32.const 0) (i32.const 120)) ;; x
    (array.set $go.bytes (local.get $arr) (i32.const 1) (i32.const 97))  ;; a
    (array.set $go.bytes (local.get $arr) (i32.const 2) (i32.const 98))  ;; b
    (array.set $go.bytes (local.get $arr) (i32.const 3) (i32.const 99))  ;; c
    (array.set $go.bytes (local.get $arr) (i32.const 4) (i32.const 121)) ;; y
    (struct.new $go.string (local.get $arr) (i32.const 1) (i32.const 3))
    (struct.new $go.string (local.get $arr) (i32.const 1) (i32.const 3))
  )

  (func (export "run") (result i32)
    (call $memequal
      (call $make3 (i32.const 97) (i32.const 98) (i32.const 99))
      (call $make3 (i32.const 97) (i32.const 98) (i32.const 99)))
  )

  (func (export "run_ne") (result i32)
    (call $memequal
      (call $make3 (i32.const 97) (i32.const 98) (i32.const 99))
      (call $make3 (i32.const 97) (i32.const 98) (i32.const 100)))
  )

  (func (export "run_sub") (result i32)
    (call $memequal (call $make_sub))
  )
)
