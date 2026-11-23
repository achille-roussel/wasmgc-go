;; Hand-written indexbyte over a $go:string header.
;; Imports compiler.panic_bounds to show asm → Go/runtime callback
;; after wasm-merge fuses the import to an export.
(module
  (type $go:object (sub (struct)))
  (type $go:bytes (array (mut i8)))
  (type $go:string (sub $go:object (struct
    (field $data (ref $go:bytes))
    (field $off  i32)
    (field $len  i32))))

  (import "compiler" "panic_bounds" (func $panic_bounds))

  (func (export "indexbyte")
        (param $s (ref $go:string))
        (param $c i32)
        (result i32)
    (local $i i32)
    (local $len i32)
    (local $data (ref $go:bytes))
    (local $off i32)

    (local.set $len (struct.get $go:string $len (local.get $s)))
    (local.set $data (struct.get $go:string $data (local.get $s)))
    (local.set $off (struct.get $go:string $off (local.get $s)))
    (local.set $i (i32.const 0))

    (block $done
      (loop $lp
        (br_if $done (i32.ge_u (local.get $i) (local.get $len)))
        (if (i32.eq
              (array.get_u $go:bytes (local.get $data)
                (i32.add (local.get $off) (local.get $i)))
              (local.get $c))
          (then (return (local.get $i))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $lp)
      )
    )
    ;; Go IndexByte returns -1 when missing; call panic_bounds only
    ;; if the caller asked for a byte with the high bit set, to prove
    ;; the fused callback is reachable without breaking the happy path.
    (if (i32.and (local.get $c) (i32.const 128))
      (then (call $panic_bounds)))
    (i32.const -1)
  )
)
