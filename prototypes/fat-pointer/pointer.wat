;; Scalar/element pointer as {accessor, base, index}, the one
;; representation docs/design/03-type-mapping.md keeps.
;;
;; The field index of a struct is an immediate inside the getter.
;; The i32 on the pointer is the array index (0 when the getter
;; ignores it). call_ref is the load.
(module
  ;; The base is (ref null any), not a struct ref. An array is not a
  ;; subtype of struct, and a scalar pointer may address either a
  ;; struct field or an array element.
  (type $box (struct (field $x (mut i32)) (field $y (mut i32))))
  (type $bytes (array (mut i8)))
  (type $geti32 (func (param anyref i32) (result i32)))
  (type $seti32 (func (param anyref i32 i32)))
  (type $class (struct
    (field $get (ref $geti32))
    (field $set (ref $seti32))))
  (type $ptr (struct
    (field $class (ref $class))
    (field $base anyref)
    (field $index i32)))

  (elem declare func $getX $setX $getByte $setByte)

  (func $getX (param $b anyref) (param $i i32) (result i32)
    (struct.get $box $x (ref.cast (ref $box) (local.get $b)))
  )
  (func $setX (param $b anyref) (param $i i32) (param $v i32)
    (struct.set $box $x (ref.cast (ref $box) (local.get $b)) (local.get $v))
  )
  (func $getByte (param $b anyref) (param $i i32) (result i32)
    (array.get_u $bytes (ref.cast (ref $bytes) (local.get $b)) (local.get $i))
  )
  (func $setByte (param $b anyref) (param $i i32) (param $v i32)
    (array.set $bytes (ref.cast (ref $bytes) (local.get $b)) (local.get $i) (local.get $v))
  )

  (func $load (param $p (ref null $ptr)) (result i32)
    (local $c (ref $class))
    (local.set $c (struct.get $ptr $class (ref.as_non_null (local.get $p))))
    (call_ref $geti32
      (struct.get $ptr $base (ref.as_non_null (local.get $p)))
      (struct.get $ptr $index (ref.as_non_null (local.get $p)))
      (struct.get $class $get (local.get $c)))
  )
  (func $store (param $p (ref null $ptr)) (param $v i32)
    (local $c (ref $class))
    (local.set $c (struct.get $ptr $class (ref.as_non_null (local.get $p))))
    (call_ref $seti32
      (struct.get $ptr $base (ref.as_non_null (local.get $p)))
      (struct.get $ptr $index (ref.as_non_null (local.get $p)))
      (local.get $v)
      (struct.get $class $set (local.get $c)))
  )

  (func (export "field") (result i32)
    (local $b (ref $box))
    (local $p (ref $ptr))
    (local.set $b (struct.new $box (i32.const 1) (i32.const 2)))
    (local.set $p
      (struct.new $ptr
        (struct.new $class (ref.func $getX) (ref.func $setX))
        (local.get $b)
        (i32.const 0)))
    (call $store (local.get $p) (i32.const 11))
    ;; The store went through the pointer; the field itself changed.
    (struct.get $box $x (local.get $b))
  )

  (func (export "elem") (result i32)
    (local $a (ref $bytes))
    (local $p (ref $ptr))
    (local.set $a
      (array.new $bytes (i32.const 0) (i32.const 4)))
    (local.set $p
      (struct.new $ptr
        (struct.new $class (ref.func $getByte) (ref.func $setByte))
        (local.get $a)
        (i32.const 2)))
    (call $store (local.get $p) (i32.const 9))
    (call $load (local.get $p))
  )

  (func (export "nil") (result i32)
    (ref.is_null (ref.null $ptr))
  )
)
