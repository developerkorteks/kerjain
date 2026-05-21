package main

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"lokerwa/internal/extractor"
)

type row struct {
	ID       string
	Status   string
	MsgType  string
	Title    string
	Company  string
	Location string
	RawText  string
}

type sample struct {
	ID     string
	Status string
	Field  string
	Stored string
	New    string
	Raw    string
}

func main() {
	db, err := sql.Open("sqlite", "file:./data/jobs.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, status, msg_type,
		       COALESCE(title, ''), COALESCE(company, ''), COALESCE(location, ''),
		       COALESCE(raw_text, '')
		FROM job_postings
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var (
		total, withRaw, validCount                                 int
		isJobFalseOnValid, titleDiffs, companyDiffs, locationDiffs int
		genericStoredTitle, genericNewTitle                        int
		samples                                                    []sample
		topNewTitles                                               = map[string]int{}
	)

	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Status, &r.MsgType, &r.Title, &r.Company, &r.Location, &r.RawText); err != nil {
			log.Fatal(err)
		}
		total++
		if r.Status == "valid" {
			validCount++
		}
		if strings.TrimSpace(r.RawText) == "" {
			continue
		}
		withRaw++

		ext := extractor.Extract(r.RawText)
		if ext.Title != "" {
			topNewTitles[normalize(ext.Title)]++
		}
		if isGenericTitle(r.Title) {
			genericStoredTitle++
		}
		if isGenericTitle(ext.Title) {
			genericNewTitle++
		}
		if r.Status == "valid" && !ext.IsJobPosting {
			isJobFalseOnValid++
			addSample(&samples, sample{
				ID: r.ID, Status: r.Status, Field: "is_job_posting",
				Stored: "valid=true", New: "extract=false", Raw: preview(r.RawText),
			})
		}

		if norm(r.Title) != norm(ext.Title) {
			titleDiffs++
			addSample(&samples, sample{
				ID: r.ID, Status: r.Status, Field: "title",
				Stored: r.Title, New: ext.Title, Raw: preview(r.RawText),
			})
		}
		if norm(r.Company) != norm(ext.Company) {
			companyDiffs++
			addSample(&samples, sample{
				ID: r.ID, Status: r.Status, Field: "company",
				Stored: r.Company, New: ext.Company, Raw: preview(r.RawText),
			})
		}
		if norm(r.Location) != norm(ext.Location) {
			locationDiffs++
			addSample(&samples, sample{
				ID: r.ID, Status: r.Status, Field: "location",
				Stored: r.Location, New: ext.Location, Raw: preview(r.RawText),
			})
		}
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("TOTAL_ROWS\t%d\n", total)
	fmt.Printf("ROWS_WITH_RAW_TEXT\t%d\n", withRaw)
	fmt.Printf("VALID_ROWS\t%d\n", validCount)
	fmt.Printf("VALID_BUT_EXTRACT_FALSE\t%d\n", isJobFalseOnValid)
	fmt.Printf("TITLE_DIFFS\t%d\n", titleDiffs)
	fmt.Printf("COMPANY_DIFFS\t%d\n", companyDiffs)
	fmt.Printf("LOCATION_DIFFS\t%d\n", locationDiffs)
	fmt.Printf("GENERIC_STORED_TITLES\t%d\n", genericStoredTitle)
	fmt.Printf("GENERIC_NEW_TITLES\t%d\n", genericNewTitle)

	fmt.Println("\nTOP_EXTRACTED_TITLES")
	type kv struct {
		Key   string
		Count int
	}
	var list []kv
	for k, v := range topNewTitles {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count == list[j].Count {
			return list[i].Key < list[j].Key
		}
		return list[i].Count > list[j].Count
	})
	for i, item := range list {
		if i == 12 {
			break
		}
		fmt.Printf("%2d. %s\t%d\n", i+1, item.Key, item.Count)
	}

	fmt.Println("\nSAMPLE_DIFFS")
	for i, s := range samples {
		if i == 15 {
			break
		}
		fmt.Printf("[%s] %s %s\n", s.Status, s.ID, s.Field)
		fmt.Printf("stored: %q\n", s.Stored)
		fmt.Printf("new   : %q\n", s.New)
		fmt.Printf("raw   : %q\n\n", s.Raw)
	}
}

func addSample(samples *[]sample, s sample) {
	if len(*samples) >= 30 {
		return
	}
	*samples = append(*samples, s)
}

func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func normalize(s string) string {
	s = norm(s)
	if s == "" {
		return "(empty)"
	}
	return s
}

func isGenericTitle(s string) bool {
	s = norm(s)
	switch {
	case s == "":
		return false
	case s == "lowongan kerja semarang":
		return true
	case s == "hi jobseeker":
		return true
	case s == "lowongan kerja":
		return true
	case strings.Contains(s, "we’re hiring"):
		return true
	case strings.Contains(s, "we are hiring"):
		return true
	case strings.HasSuffix(s, "hiring"):
		return true
	default:
		return false
	}
}
