package main

import (
	"context"
	"time"
)

// Recoverable physical deletion. Tombstones remain in SQLite, so an upstream outage never restores visibility.
func (a *App) cleanupOpenIM(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.cleanupPass(ctx)
		}
	}
}
func (a *App) cleanupPass(parent context.Context) {
	// Network retries must not hold the mutation lock: users must still be able to disable an account during an OpenIM outage.
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	rows, err := a.db.Query("SELECT id FROM users WHERE role='customer'")
	if err != nil {
		return
	}
	var customers []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		customers = append(customers, id)
	}
	rows.Close()
	for _, customer := range customers {
		if ctx.Err() != nil {
			return
		}
		conv := conversationID(customer, a.cfg.AgentID)
		var upto, cursor int64
		if a.db.QueryRow("SELECT upto,cursor FROM purge_ranges WHERE customer=?", customer).Scan(&upto, &cursor) == nil && cursor <= upto {
			end := cursor + 99
			if end > upto {
				end = upto
			}
			seqs := make([]int64, 0, end-cursor+1)
			for i := cursor; i <= end; i++ {
				seqs = append(seqs, i)
			}
			if a.im.delete(ctx, conv, seqs) == nil {
				_, _ = a.db.Exec("UPDATE purge_ranges SET cursor=max(cursor,?) WHERE customer=?", end+1, customer)
			}
		}
		var pending int
		_ = a.db.QueryRow("SELECT count(*) FROM sends WHERE customer=? AND (state IN ('accepted','sending','uncertain','deleted_pending') OR (state='delivered' AND seq=0))", customer).Scan(&pending)
		if pending > 0 {
			a.reconcile(ctx, customer, conv)
		}
		dr, e := a.db.Query("SELECT d.seq FROM deletions d LEFT JOIN deletion_done done ON done.customer=d.customer AND done.seq=d.seq WHERE d.customer=? AND done.seq IS NULL LIMIT 100", customer)
		if e != nil {
			continue
		}
		var dels []int64
		for dr.Next() {
			var seq int64
			_ = dr.Scan(&seq)
			dels = append(dels, seq)
		}
		dr.Close()
		if len(dels) > 0 && a.im.delete(ctx, conv, dels) == nil {
			for _, seq := range dels {
				_, _ = a.db.Exec("INSERT OR IGNORE INTO deletion_done(customer,seq) VALUES(?,?)", customer, seq)
			}
		}
	}
}
func (a *App) reconcile(ctx context.Context, customer, conv string) {
	maxes, e := a.im.maxSeqs(ctx, customer)
	if e != nil {
		return
	}
	maxSeq := maxes[conv]
	if maxSeq < 1 {
		return
	}
	scan := int64(1)
	_ = a.db.QueryRow("SELECT cursor FROM sync_cursors WHERE customer=?", customer).Scan(&scan)
	end := scan + 99
	if end > maxSeq {
		end = maxSeq
	}
	begin := scan
	if begin > end {
		begin = end - 49
		if begin < 1 {
			begin = 1
		}
	}
	remote, e := a.im.pull(ctx, customer, conv, begin, end)
	if e != nil {
		return
	}
	for _, m := range remote {
		p, ok := decodeMessage(m)
		if !ok {
			continue
		}
		var state string
		_ = a.db.QueryRow("SELECT state FROM sends WHERE id=? AND customer=?", p.ID, customer).Scan(&state)
		if state == "deleted" || state == "deleted_pending" {
			_, _ = a.db.Exec("INSERT OR IGNORE INTO deletions(customer,seq) VALUES(?,?)", customer, m.Seq)
			_, _ = a.db.Exec("UPDATE sends SET state='deleted' WHERE id=? AND state='deleted_pending'", p.ID)
		} else if state != "" {
			res, err := a.db.Exec("UPDATE sends SET state='delivered',remote_id=?,seq=? WHERE id=? AND state IN ('sending','accepted','uncertain','delivered') AND seq=0", m.ClientID, m.Seq, p.ID)
			if err == nil {
				n, _ := res.RowsAffected()
				if n > 0 {
					a.publish()
				}
			}
		}
	}
	if len(remote) > 0 {
		_, _ = a.db.Exec("INSERT INTO sync_cursors(customer,cursor) VALUES(?,?) ON CONFLICT(customer) DO UPDATE SET cursor=max(cursor,excluded.cursor)", customer, end+1)
	}
}
