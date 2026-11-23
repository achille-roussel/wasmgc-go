package runtime

// selectGo implements the select algorithm (04). Not a 1 ms poll.
//
// 1. Shuffle cases with fastrand.
// 2. Probe each without parking; first that can proceed wins.
// 3. Default if present.
// 4. Else enqueue one waiter per case and park once.
// 5. Partner sets winner and readies; on wake dequeue other waiters.
//
// scase / select_ / trySelectReady live in chan.go so programs that only
// use channels need not link this file (rtlink.EnsureSched).

// selectGo runs the algorithm. Returns the winning case index and, for a
// receive win, the received value and ok. The lowerer binds assign-form
// recv cases from those return values (reliable across park).
func selectGo(cases []scase) (chosen int32, recv any, recvOK bool) {
	n := int32(len(cases))
	if n == 0 {
		gopark(mintParkID(), waitReasonChanSelect)
		return -1, nil, false
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
		cs := cases[oi]
		kind := cs.kind
		if kind == 2 {
			def = oi
		} else if kind == 1 {
			if chanSend(cs.c, cs.elem, cs.ops, false) {
				return oi, nil, false
			}
		} else if kind == 0 {
			v, ok, proceed := chanRecvNonblock(cs.c, cs.ops)
			if proceed {
				cs.elem = v
				cs.ok = ok
				cases[oi] = cs
				return oi, v, ok
			}
		}
	}
	if def >= 0 {
		return def, nil, false
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
		cs := cases[i]
		if cs.kind == 2 || cs.c == nil {
			continue
		}
		w := &waiter{park: sel.park, sel: sel, casi: i, isSend: cs.kind == 1, cas: &cases[i]}
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
	var out any
	var outOK bool
	for i := int32(0); i < n; i++ {
		w := waiters[i]
		if w == nil {
			continue
		}
		cs := cases[i]
		if i != win {
			if cs.kind == 1 {
				cs.c.sendq.remove(w)
			} else {
				cs.c.recvq.remove(w)
			}
			continue
		}
		if cs.kind == 0 {
			if cs.c.closed != 0 && w.elem == nil {
				out = cs.ops.zero()
				outOK = false
			} else {
				out = w.elem
				outOK = true
			}
			cs.elem = out
			cs.ok = outOK
			cases[i] = cs
			if w.cas != nil {
				w.cas.elem = out
				w.cas.ok = outOK
			}
		}
	}
	if gp != nil {
		gp.sel = nil
	}
	if win < 0 {
		throw("select: no winner after park")
	}
	return win, out, outOK
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
