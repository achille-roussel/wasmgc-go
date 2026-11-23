;; M5 runtime driver: deferRun LIFO, recoverOk protocol, unrecovered panic → exit 2.
;; Hand-written per docs/design/09 (stream F tests via harness, not the compiler).
;; Parse: wasm-tools parse -t m5_defer_panic.wat -o m5_defer_panic.wasm
;; Run:   node run_m5.mjs
(module
  ;; Prelude shapes (03 / 04). Simplified iface as i32 payload for the driver.
  (type $go.object (sub (struct)))
  (type $deferFn (func))
  (type $panicObj (sub $go.object (struct
    (field $value     i32)
    (field $recovered (mut i32))
    (field $next      (mut (ref null $panicObj))))))

  (rec
    (type $defer (sub $go.object (struct
      (field $fn   (ref $deferFn))
      (field $link (mut (ref null $defer))))))
  )
  (type $g (sub $go.object (struct
    (field $recoverOk (mut i32)))))

  (tag $go.panic (param (ref $panicObj)))

  (elem declare func $deferA $deferB $deferRecoverThunk)

  (global $g (mut (ref null $g)) (ref.null $g))
  (global $printed (mut i32) (i32.const 0))
  (global $exitCode (mut i32) (i32.const -1))

  ;; Host stand-ins: record print / exit instead of real bridge.
  (func $notePrint (param $v i32)
    (global.set $printed
      (i32.add (global.get $printed) (local.get $v))))
  (func $noteExit (param $code i32)
    (global.set $exitCode (local.get $code)))

  ;; runtime.deferRun: LIFO, set recoverOk=1 around each call, clear after.
  (func $runtime.deferRun (param $d (ref null $defer))
    (local $fn (ref $deferFn))
    (local $gg (ref $g))
    (block $done
      (loop $walk
        (br_if $done (ref.is_null (local.get $d)))
        (local.set $fn (struct.get $defer $fn (local.get $d)))
        (local.set $d (struct.get $defer $link (local.get $d)))
        (local.set $gg (ref.as_non_null (global.get $g)))
        (struct.set $g $recoverOk (local.get $gg) (i32.const 1))
        (call_ref $deferFn (local.get $fn))
        (struct.set $g $recoverOk (local.get $gg) (i32.const 0))
        (br $walk)
      )
    )
  )

  ;; Unrecovered path: print value marker + exit 2.
  (func $runtime.unrecoveredPanic (param $p (ref $panicObj))
    (call $notePrint (struct.get $panicObj $value (local.get $p)))
    (call $noteExit (i32.const 2))
  )

  ;; Deferred closure: records 1.
  (func $deferA
    (call $notePrint (i32.const 1)))
  ;; Deferred closure: records 10.
  (func $deferB
    (call $notePrint (i32.const 10)))

  ;; Entry: push B then A (so LIFO runs A then B → printed 1+10=11), return.
  (func (export "defer_lifo") (result i32)
    (local $d (ref null $defer))
    (global.set $g (struct.new $g (i32.const 0)))
    (global.set $printed (i32.const 0))
    ;; d = {fn:B, link:null}; d = {fn:A, link:d}
    (local.set $d
      (struct.new $defer
        (ref.func $deferB)
        (ref.null $defer)))
    (local.set $d
      (struct.new $defer
        (ref.func $deferA)
        (local.get $d)))
    (call $runtime.deferRun (local.get $d))
    (global.get $printed)
  )

  ;; Unrecovered panic: catch_all style entry prints value and exits 2.
  (func (export "unrecovered") (result i32)
    (local $p (ref $panicObj))
    (local $ex exnref)
    (global.set $printed (i32.const 0))
    (global.set $exitCode (i32.const -1))
    (block $out (result i32)
      (block $handler (result (ref $panicObj) exnref)
        (try_table (catch_ref $go.panic $handler)
          (throw $go.panic
            (struct.new $panicObj (i32.const 42) (i32.const 0) (ref.null $panicObj)))
          (i32.const 0)
          (br $out)
        )
        unreachable
      )
      (local.set $ex)
      (local.set $p)
      (if (i32.eqz (struct.get $panicObj $recovered (local.get $p)))
        (then
          (call $runtime.unrecoveredPanic (local.get $p))
          (drop (local.get $ex))
          (br $out (global.get $exitCode))
        )
      )
      (throw_ref (local.get $ex))
      unreachable
    )
  )

  ;; recoverOk: a "deferred" fn that reads recoverOk then clears it (prologue).
  (func $deferredRecoverProbe (result i32)
    (local $gg (ref $g))
    (local $allow i32)
    (local.set $gg (ref.as_non_null (global.get $g)))
    (local.set $allow (struct.get $g $recoverOk (local.get $gg)))
    (struct.set $g $recoverOk (local.get $gg) (i32.const 0))
    (local.get $allow)
  )

  (func (export "recover_ok") (result i32)
    (local $d (ref null $defer))
    (global.set $g (struct.new $g (i32.const 0)))
    ;; Wrap probe as deferFn via a thunk that stores allow into $printed.
    (local.set $d
      (struct.new $defer
        (ref.func $deferRecoverThunk)
        (ref.null $defer)))
    (call $runtime.deferRun (local.get $d))
    (global.get $printed)
  )

  (func $deferRecoverThunk
    (global.set $printed (call $deferredRecoverProbe)))
)
