// migrate_lid resolves @lid sender JIDs to real phone numbers for existing DB records.
// Run once: go run ./cmd/migrate_lid/
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func main() {
	ctx := context.Background()

	// Open whatsapp session
	container, err := sqlstore.New(ctx, "sqlite", "file:./data/whatsapp.db?_pragma=foreign_keys(1)", waLog.Noop)
	if err != nil {
		log.Fatalf("open wa store: %v", err)
	}
	devices, err := container.GetAllDevices(ctx)
	if err != nil || len(devices) == 0 {
		log.Fatalf("no devices: %v", err)
	}
	lidStore := devices[0].LIDs

	// Open jobs DB
	jdb, err := sql.Open("sqlite", "./data/jobs.db")
	if err != nil {
		log.Fatalf("open jobs db: %v", err)
	}
	defer jdb.Close()

	// Fetch all unique @lid JIDs
	rows, err := jdb.QueryContext(ctx, `SELECT DISTINCT sender_jid FROM job_postings WHERE sender_jid LIKE '%@lid'`)
	if err != nil {
		log.Fatalf("query: %v", err)
	}

	type result struct {
		lid   string
		phone string
		ok    bool
	}
	var resolved []result

	for rows.Next() {
		var jidStr string
		rows.Scan(&jidStr)

		userPart := jidStr[:strings.Index(jidStr, "@")]
		if idx := strings.Index(userPart, ":"); idx > 0 {
			userPart = userPart[:idx]
		}
		lid := types.JID{User: userPart, Server: "lid"}

		pn, err := lidStore.GetPNForLID(ctx, lid)
		if err != nil || pn.IsEmpty() {
			resolved = append(resolved, result{lid: jidStr, ok: false})
			continue
		}
		phone := pn.User
		resolved = append(resolved, result{lid: jidStr, phone: phone, ok: true})
	}
	rows.Close()

	ok, fail := 0, 0
	for _, r := range resolved {
		if !r.ok {
			fmt.Printf("  SKIP  %s (not in LID store)\n", r.lid)
			fail++
			continue
		}
		// Update sender_jid to @s.whatsapp.net and sender_phone
		newJID := r.phone + "@s.whatsapp.net"
		res, err := jdb.ExecContext(ctx,
			`UPDATE job_postings SET sender_jid=?, sender_phone=? WHERE sender_jid=?`,
			newJID, r.phone, r.lid,
		)
		if err != nil {
			fmt.Printf("  ERROR %s: %v\n", r.lid, err)
			fail++
			continue
		}
		n, _ := res.RowsAffected()
		fmt.Printf("  OK    %s → %s (%d rows)\n", r.lid, newJID, n)
		ok++
	}

	fmt.Printf("\nDone: %d resolved, %d skipped/failed\n", ok, fail)
}
