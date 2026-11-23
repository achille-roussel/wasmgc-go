package runtime

// selectGo implements the select algorithm (04). Not a 1 ms poll.
//
// 1. Shuffle cases with fastrand.
// 2. Probe each without parking; first that can proceed wins.
// 3. Default if present.
// 4. Else enqueue one waiter per case and park once.
// 5. Partner sets winner and readies; on wake dequeue other waiters.

// scase is one select case. Kind: 0=recv, 1=send, 2=default.
type scase struct {
	kind int32
	c    *hchan
	ops  *chanOps
	elem any // send value, or recv result after win
	ok   bool
}

// select_ is the park object shared by waiters of one select.
type select_ struct {
	park   int32
	winner int32 // case index, or -1
	ncase  int32
	slice  []scase
	done   int32 // 1 once a case won
}

// selectGo runs the algorithm. Returns the winning case index.
func selectGo(cases []scase) int32 {
	n := int32(len(cases))
	if n == 0 {
		gopark(mintParkID(), waitReasonChanSelect)
		return -1
	}

	order := make([]int32, n)
	for i := int32(0); i < n; i++ {
		order[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := int32(fastrandn(uint32(i + 1)))
		order[i], order[j] = order[j], order[i]
	}

	var def int32 = -1
	for _, oi := range order {
		cs := &cases[oi]
		switch cs.kind {
		case 2:
			def = oi
		case 1:
			if chanSend(cs.c, cs.elem, cs.ops, false) {
				return oi
			}
		case 0:
			v, ok, proceed := chanRecvNonblock(cs.c, cs.ops)
			if proceed {
				cs.elem = v
				cs.ok = ok
				return oi
			}
		}
	}
	if def >= 0 {
		return def
	}

	sel := &select_{
		park:   mintParkID(),
		winner: -1,
		ncase:  n,
		slice:  cases,
	}
	if gp != nil {
		gp.sel = sel
	}
	waiters := make([]*waiter, n)
	for i := int32(0); i < n; i++ {
		cs := &cases[i]
		if cs.kind == 2 || cs.c == nil {
			continue
		}
		w := &waiter{park: sel.park, sel: sel, casi: i, isSend: cs.kind == 1, cas: cs}
		if cs.kind == 1 {
			cs.ops.toWaiter(w, cs.elem)
		}
		waiters[i] = w
		if cs.kind == 1 {
			cs.c.sendq.enqueue(w)
		} else {
			cs.c.recvq.enqueue(w)
		}
	}
	gopark(sel.park, waitReasonChanSelect)

	win := sel.winner
	for i := int32(0); i < n; i++ {
		w := waiters[i]
		if w == nil {
			continue
		}
		cs := cases[i] // ref-typed struct; do not take &cases[i] (no alias)
		if i != win {
			if cs.kind == 1 {
				cs.c.sendq.remove(w)
			} else {
				cs.c.recvq.remove(w)
			}
			continue
		}
		if cs.kind == 0 {
			// Assign through the local ref (same object as cases[i] in the array).
			if cs.c.closed != 0 && w.elem == nil {
				cs.elem = cs.ops.zero()
				cs.ok = false
			} else {
				cs.elem = w.elem
				cs.ok = true
			}
		}
	}
	if gp != nil {
		gp.sel = nil
	}
	if win < 0 {
		throw("select: no winner after park")
	}
	return win
}

func chanRecvNonblock(c *hchan, ops *chanOps) (any, bool, bool) {
	if c == nil {
		return nil, false, false
	}
	if c.sendq.head != nil || c.qcount > 0 {
		v, ok := chanRecv(c, ops, false)
		return v, ok, true
	}
	if c.closed != 0 {
		return ops.zero(), false, true
	}
	return nil, false, false
}

// trySelectReady claims sel for waiter w. Returns true if this waiter won.
func trySelectReady(w *waiter) bool {
	sel := w.sel
	if sel == nil {
		return true
	}
	if sel.done != 0 {
		return false
	}
	sel.done = 1
	sel.winner = w.casi
	return true
}
