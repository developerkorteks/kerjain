package main

import (
	"fmt"
	"lokerwa/internal/extractor"
)

func main() {
	cases := []string{
		"Dibutuhkan segera Tenaga jualan di warung kecil",
		"Di butuhkan volunteer ticketing ( Cewe ) event",
		"Need Room attendant URGENT For today",
		"Butuh 1 karyawati untuk jaga warung proyek",
		"Urgent !!! Butuh karyawati jaga warung",
		"chat biasa halo apa kabar teman",
		"oke siap noted",
		"lowongan kerja staff gudang pria max 35 tahun",
		"butuh urgent 1.kasir 2.waiters 3.bartender",
	}
	for _, t := range cases {
		j := extractor.Extract(t)
		mark := "✗"
		if j.IsJobPosting {
			mark = "✓"
		}
		preview := t
		if len(preview) > 55 {
			preview = preview[:55]
		}
		fmt.Printf("%s is_job=%-5v title=%-30q  %q\n", mark, j.IsJobPosting, j.Title, preview)
	}
}
