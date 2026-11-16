package store

import (
	"context"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/model"
)

func TestRollupKeepsTotalsAndShrinksHistory(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now().Truncate(time.Hour).Add(20 * time.Minute)

	// Three days of traffic, one connection a minute to each of two places,
	// from the top of an hour so that every hour includes its first minute.
	var batch []model.FlowDelta
	for at := now.Add(-72 * time.Hour).Truncate(time.Hour); at.Before(now); at = at.Add(time.Minute) {
		for _, site := range []string{"a.example", "b.example"} {
			batch = append(batch, model.FlowDelta{Bucket: at.Unix(), ClientID: "vm-100", ClientIP: "10.0.0.2", ServerIP: "198.51.100.1",
				ServerPort: 443, Proto: "tcp", Domain: site, BytesUp: 10, BytesDown: 100, Packets: 2, Conns: 1,
				FirstSeen: at.UnixMilli(), LastSeen: at.UnixMilli() + 500})
		}
	}
	if err := st.Apply(batch); err != nil {
		t.Fatal(err)
	}
	rows := func() (n int) {
		st.db.QueryRow(`SELECT COUNT(*) FROM flows`).Scan(&n)
		return n
	}
	type totals struct {
		sent, received, conns, first, last int64
		points                             int
	}
	measure := func() totals {
		t.Helper()
		since := now.Add(-80 * time.Hour)
		var out totals
		peers, err := st.Peers(ctx, since, "", ScopeAll, 100)
		if err != nil || len(peers) != 2 {
			t.Fatalf("%d peers, %v", len(peers), err)
		}
		for _, p := range peers {
			out.sent += p.Sent
			out.received += p.Received
			out.conns += p.Conns
			out.first, out.last = p.FirstSeen, p.LastSeen
		}
		tl, err := st.Timeline(ctx, since, now, "", ScopeAll)
		if err != nil {
			t.Fatal(err)
		}
		var sent int64
		for _, p := range tl.Points {
			sent += p.Sent
			if p.Sent > 0 {
				out.points++
			}
		}
		if sent != out.sent {
			t.Errorf("timeline adds up to %d, peers to %d", sent, out.sent)
		}
		return out
	}

	before, rowsBefore := measure(), rows()
	if rowsBefore != len(batch) {
		t.Fatalf("%d rows for %d records", rowsBefore, len(batch))
	}

	// Keep one day of minute detail.
	cutoff := now.Add(-24 * time.Hour)
	if err := st.Rollup(cutoff); err != nil {
		t.Fatal(err)
	}
	after, rowsAfter := measure(), rows()
	if after != before {
		t.Errorf("totals changed:\n before %+v\n after  %+v", before, after)
	}
	// 48 hours folded to one row per place per hour; 24 h 20 min left alone.
	wantRows := 2*48 + 2*(24*60+20)
	if rowsAfter != wantRows {
		t.Errorf("%d rows after rollup, want %d (was %d)", rowsAfter, wantRows, rowsBefore)
	}
	var partHours, recentMinutes int
	edge := cutoff.Unix() / 3600 * 3600
	st.db.QueryRow(`SELECT COUNT(*) FROM flows WHERE bucket < ? AND bucket % 3600 != 0`, edge).Scan(&partHours)
	st.db.QueryRow(`SELECT COUNT(*) FROM flows WHERE bucket >= ? AND bucket % 3600 != 0`, edge).Scan(&recentMinutes)
	if partHours != 0 || recentMinutes == 0 {
		t.Errorf("%d old rows are not on the hour; %d recent minute rows remain", partHours, recentMinutes)
	}

	// Running it again, as happens every hour, changes nothing.
	if err := st.Rollup(cutoff); err != nil {
		t.Fatal(err)
	}
	if again := measure(); again != before || rows() != rowsAfter {
		t.Errorf("a second rollup changed things: %+v, %d rows", again, rows())
	}

	// Tidy applies both periods: fold after one day, delete after two.
	if err := st.Tidy(now, Settings{DetailDays: 1, RetentionDays: 2}); err != nil {
		t.Fatal(err)
	}
	var oldest int64
	st.db.QueryRow(`SELECT MIN(bucket) FROM flows`).Scan(&oldest)
	if age := now.Sub(time.Unix(oldest, 0)); age > 49*time.Hour || age < 47*time.Hour {
		t.Errorf("oldest history is %v old, want about two days", age)
	}
}
