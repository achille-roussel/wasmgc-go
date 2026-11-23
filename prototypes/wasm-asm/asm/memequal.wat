;; Hand-written Wasm assembly: string memequal.
;;
;; Uses symbolic $go:… names resolved by run.mjs to $go.… before
;; wasm-tools parse. Field layout matches docs/design/03-type-mapping.md:
;;   $go.string = { data (ref $go.bytes), off i32, len i32 }
;;
;; Bound, in a real build, to a body-less Go declaration:
;;
;;   //go:noescape
;;   func MemEqual(a, b string) bool
;;
(module
  (type $go:object (sub (struct)))
  (type $go:bytes (array (mut i8)))
  (type $go:string (sub $go:object (struct
    (field $data (ref $go:bytes))
    (field $off  i32)
    (field $len  i32))))

  (func (export "memequal")
        (param $a (ref $go:string))
        (param $b (ref $go:string))
        (result i32)
    (local $i i32)
    (local $len i32)
    (local $da (ref $go:bytes))
    (local $db (ref $go:bytes))
    (local $oa i32)
    (local $ob i32)

    (local.set $len (struct.get $go:string $len (local.get $a)))
    (if (i32.ne (local.get $len) (struct.get $go:string $len (local.get $b)))
      (then (return (i32.const 0))))

    (local.set $da (struct.get $go:string $data (local.get $a)))
    (local.set $db (struct.get $go:string $data (local.get $b)))
    (local.set $oa (struct.get $go:string $off (local.get $a)))
    (local.set $ob (struct.get $go:string $off (local.get $b)))
    (local.set $i (i32.const 0))

    (block $done
      (loop $lp
        (br_if $done (i32.ge_u (local.get $i) (local.get $len)))
        (if (i32.ne
              (array.get_u $go:bytes (local.get $da)
                (i32.add (local.get $oa) (local.get $i)))
              (array.get_u $go:bytes (local.get $db)
                (i32.add (local.get $ob) (local.get $i))))
          (then (return (i32.const 0))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $lp)
      )
    )
    (i32.const 1)
  )
)
