;; M6 map scaffold driver: i32 and string-key lookups through shared helpers.
;; Fills until real mapAssign/mapAccess land; documents the call shape (04, 09).
(module
  (type $go.object (sub (struct)))
  (type $go.bytes (array (mut i8)))
  (type $go.string (sub $go.object (struct
    (field $data (ref $go.bytes))
    (field $off  i32)
    (field $len  i32))))

  ;; Tiny fixed table: 4 i32 keys / vals. Empty slot key = -1.
  (type $i32arr (array (mut i32)))
  (type $i32tab (struct
    (field $keys (mut (ref $i32arr)))
    (field $vals (mut (ref $i32arr)))
    (field $used (mut i32))))

  (func $mapAssignI32 (param $t (ref $i32tab)) (param $k i32) (param $v i32)
    (local $i i32)
    (local $keys (ref $i32arr))
    (local.set $keys (struct.get $i32tab $keys (local.get $t)))
    (local.set $i (i32.const 0))
    (block $done
      (loop $scan
        (br_if $done (i32.ge_u (local.get $i) (array.len (local.get $keys))))
        (if (i32.eq (array.get $i32arr (local.get $keys) (local.get $i)) (local.get $k))
          (then
            (array.set $i32arr (struct.get $i32tab $vals (local.get $t))
              (local.get $i) (local.get $v))
            (br $done)
          )
        )
        (if (i32.eq (array.get $i32arr (local.get $keys) (local.get $i)) (i32.const -1))
          (then
            (array.set $i32arr (local.get $keys) (local.get $i) (local.get $k))
            (array.set $i32arr (struct.get $i32tab $vals (local.get $t))
              (local.get $i) (local.get $v))
            (struct.set $i32tab $used (local.get $t)
              (i32.add (struct.get $i32tab $used (local.get $t)) (i32.const 1)))
            (br $done)
          )
        )
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $scan)
      )
    )
  )

  (func $mapAccessI32 (param $t (ref $i32tab)) (param $k i32) (result i32)
    (local $i i32)
    (local $keys (ref $i32arr))
    (local.set $keys (struct.get $i32tab $keys (local.get $t)))
    (local.set $i (i32.const 0))
    (block $miss (result i32)
      (loop $scan
        (br_if $miss (i32.const -999)
          (i32.ge_u (local.get $i) (array.len (local.get $keys))))
        (if (i32.eq (array.get $i32arr (local.get $keys) (local.get $i)) (local.get $k))
          (then
            (br $miss
              (array.get $i32arr (struct.get $i32tab $vals (local.get $t)) (local.get $i)))
          )
        )
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $scan)
      )
      (i32.const -999)
    )
  )

  (func (export "i32_roundtrip") (result i32)
    (local $t (ref $i32tab))
    (local.set $t
      (struct.new $i32tab
        (array.new $i32arr (i32.const -1) (i32.const 4))
        (array.new $i32arr (i32.const 0) (i32.const 4))
        (i32.const 0)))
    (call $mapAssignI32 (local.get $t) (i32.const 7) (i32.const 42))
    (call $mapAssignI32 (local.get $t) (i32.const 3) (i32.const 99))
    ;; expect 42
    (call $mapAccessI32 (local.get $t) (i32.const 7))
  )

  ;; String key: compare len then first byte (scaffold, not full eq).
  (func $strEq (param $a (ref $go.string)) (param $b (ref $go.string)) (result i32)
    (if (i32.ne (struct.get $go.string $len (local.get $a))
                 (struct.get $go.string $len (local.get $b)))
      (then (return (i32.const 0)))
    )
    (if (i32.eqz (struct.get $go.string $len (local.get $a)))
      (then (return (i32.const 1)))
    )
    (i32.eq
      (array.get_u $go.bytes
        (struct.get $go.string $data (local.get $a))
        (struct.get $go.string $off (local.get $a)))
      (array.get_u $go.bytes
        (struct.get $go.string $data (local.get $b))
        (struct.get $go.string $off (local.get $b))))
  )

  (func (export "string_eq_smoke") (result i32)
    (local $a (ref $go.string))
    (local $b (ref $go.string))
    (local $bytes (ref $go.bytes))
    (local.set $bytes (array.new_fixed $go.bytes 2 (i32.const 0x61) (i32.const 0x62))) ;; "ab"
    (local.set $a (struct.new $go.string (local.get $bytes) (i32.const 0) (i32.const 2)))
    (local.set $b (struct.new $go.string (local.get $bytes) (i32.const 0) (i32.const 2)))
    (call $strEq (local.get $a) (local.get $b))
  )
)
