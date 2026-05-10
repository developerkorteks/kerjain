package storage

import (
	"os"
	"testing"
	"time"

	"lokerwa/internal/extractor"
)

func newTestDB(t *testing.T) *Storage {
	t.Helper()
	f, err := os.CreateTemp("", "jobs_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	s, err := New(f.Name())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func job(id, status, msgType string) *extractor.JobPosting {
	return &extractor.JobPosting{
		ID:           id,
		SourceGroup:  "120363@g.us",
		GroupName:    "Test Group",
		SenderJID:    "628123@s.whatsapp.net",
		SenderName:   "Tester",
		SenderPhone:  "628123",
		MsgType:      msgType,
		RawText:      "Dibutuhkan staff toko",
		PostedAt:     time.Now().Format(time.RFC3339),
		IsJobPosting: true,
		Title:        "Staff Toko",
		Status:       status,
	}
}

// ── Save & List ───────────────────────────────────────────────────────────────

func TestSaveAndList(t *testing.T) {
	s := newTestDB(t)

	if err := s.Save(job("id1", "raw", "text")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(job("id2", "review", "text")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(job("id3", "valid", "image")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	page, err := s.List(Filter{Limit: 10, Page: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("expected total=3, got %d", page.Total)
	}
	if len(page.Jobs) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(page.Jobs))
	}
}

// ── Duplicate ID silently ignored ─────────────────────────────────────────────

func TestSave_DuplicateIgnored(t *testing.T) {
	s := newTestDB(t)
	if err := s.Save(job("dup", "raw", "text")); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(job("dup", "valid", "text")); err != nil {
		t.Fatal(err)
	}
	page, _ := s.List(Filter{Limit: 10, Page: 1})
	if page.Total != 1 {
		t.Errorf("expected total=1 (duplicate ignored), got %d", page.Total)
	}
	// status should remain "raw" (first insert wins)
	if page.Jobs[0].Status != "raw" {
		t.Errorf("expected status=raw (first insert), got %q", page.Jobs[0].Status)
	}
}

// ── Filter by status ──────────────────────────────────────────────────────────

func TestList_FilterStatus(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("a", "raw", "text"))
	s.Save(job("b", "raw", "text"))
	s.Save(job("c", "review", "text"))
	s.Save(job("d", "valid", "text"))
	s.Save(job("e", "valid", "image"))

	cases := []struct {
		status string
		want   int
	}{
		{"raw", 2},
		{"review", 1},
		{"valid", 2},
		{"", 5},
	}
	for _, tc := range cases {
		p, err := s.List(Filter{Status: tc.status, Limit: 10, Page: 1})
		if err != nil {
			t.Fatalf("List status=%q: %v", tc.status, err)
		}
		if p.Total != tc.want {
			t.Errorf("status=%q → total=%d, want %d", tc.status, p.Total, tc.want)
		}
	}
}

// ── Filter by msg_type ────────────────────────────────────────────────────────

func TestList_FilterMsgType(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("t1", "raw", "text"))
	s.Save(job("t2", "raw", "text"))
	s.Save(job("i1", "raw", "image"))

	cases := []struct {
		typ  string
		want int
	}{
		{"text", 2},
		{"image", 1},
		{"", 3},
	}
	for _, tc := range cases {
		p, _ := s.List(Filter{MsgType: tc.typ, Limit: 10, Page: 1})
		if p.Total != tc.want {
			t.Errorf("type=%q → total=%d, want %d", tc.typ, p.Total, tc.want)
		}
	}
}

// ── Filter by group ───────────────────────────────────────────────────────────

func TestList_FilterGroup(t *testing.T) {
	s := newTestDB(t)
	j1 := job("g1", "raw", "text")
	j1.SourceGroup = "group-A@g.us"
	j2 := job("g2", "raw", "text")
	j2.SourceGroup = "group-B@g.us"
	s.Save(j1)
	s.Save(j2)

	p, _ := s.List(Filter{Group: "group-A@g.us", Limit: 10, Page: 1})
	if p.Total != 1 {
		t.Errorf("group filter → total=%d, want 1", p.Total)
	}
}

// ── Pagination ────────────────────────────────────────────────────────────────

func TestList_Pagination(t *testing.T) {
	s := newTestDB(t)
	for i := range 7 {
		s.Save(job("pg"+string(rune('a'+i)), "raw", "text"))
	}

	p1, _ := s.List(Filter{Limit: 3, Page: 1})
	if len(p1.Jobs) != 3 {
		t.Errorf("page1 jobs=%d, want 3", len(p1.Jobs))
	}
	if p1.Total != 7 {
		t.Errorf("total=%d, want 7", p1.Total)
	}

	p3, _ := s.List(Filter{Limit: 3, Page: 3})
	if len(p3.Jobs) != 1 {
		t.Errorf("page3 jobs=%d, want 1 (last page)", len(p3.Jobs))
	}

	p4, _ := s.List(Filter{Limit: 3, Page: 4})
	if len(p4.Jobs) != 0 {
		t.Errorf("page4 jobs=%d, want 0 (beyond end)", len(p4.Jobs))
	}
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestGetByID(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("myid", "raw", "text"))

	j, err := s.GetByID("myid")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if j.ID != "myid" {
		t.Errorf("ID=%q, want %q", j.ID, "myid")
	}
	if j.SenderPhone != "628123" {
		t.Errorf("SenderPhone=%q, want %q", j.SenderPhone, "628123")
	}

	_, err = s.GetByID("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// ── UpdateJob ─────────────────────────────────────────────────────────────────

func TestUpdateJob(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("upd", "raw", "text"))

	patch := JobPatch{
		Status:       "valid",
		IsJobPosting: true,
		Title:        "Admin Gudang",
		Company:      "PT Test",
		Location:     "Semarang",
		Gender:       "female",
		AgeMax:       35,
		Education:    "SMA",
		Salary:       "3.000.000",
		Contact:      "0812345678",
		ContactType:  "wa",
		Requirements: []string{"Jujur", "Disiplin"},
		Benefits:     []string{"BPJS", "Makan siang"},
	}
	if err := s.UpdateJob("upd", patch); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	j, _ := s.GetByID("upd")
	if j.Status != "valid" {
		t.Errorf("status=%q, want valid", j.Status)
	}
	if j.Title != "Admin Gudang" {
		t.Errorf("title=%q, want Admin Gudang", j.Title)
	}
	if j.Company != "PT Test" {
		t.Errorf("company=%q", j.Company)
	}
	if j.AgeMax != 35 {
		t.Errorf("age_max=%d, want 35", j.AgeMax)
	}
	if len(j.Requirements) != 2 {
		t.Errorf("requirements len=%d, want 2", len(j.Requirements))
	}
	if len(j.Benefits) != 2 {
		t.Errorf("benefits len=%d, want 2", len(j.Benefits))
	}
}

// ── UpdateJob status cycle ────────────────────────────────────────────────────

func TestUpdateJob_StatusCycle(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("cyc", "raw", "text"))

	for _, status := range []string{"review", "valid", "raw"} {
		if err := s.UpdateJob("cyc", JobPatch{Status: status}); err != nil {
			t.Fatalf("UpdateJob status=%q: %v", status, err)
		}
		j, _ := s.GetByID("cyc")
		if j.Status != status {
			t.Errorf("after update: status=%q, want %q", j.Status, status)
		}
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestDelete(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("del", "raw", "text"))

	if err := s.Delete("del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.GetByID("del")
	if err == nil {
		t.Error("expected error after delete")
	}

	p, _ := s.List(Filter{Limit: 10, Page: 1})
	if p.Total != 0 {
		t.Errorf("expected 0 after delete, got %d", p.Total)
	}
}

// ── SenderPhone persisted ─────────────────────────────────────────────────────

func TestSenderPhone_Persisted(t *testing.T) {
	s := newTestDB(t)
	j := job("ph1", "raw", "text")
	j.SenderPhone = "6282112345678"
	s.Save(j)

	got, _ := s.GetByID("ph1")
	if got.SenderPhone != "6282112345678" {
		t.Errorf("SenderPhone=%q, want 6282112345678", got.SenderPhone)
	}
}

// ── Combined status+type filter ───────────────────────────────────────────────

func TestList_CombinedFilter(t *testing.T) {
	s := newTestDB(t)
	s.Save(job("c1", "valid", "text"))
	s.Save(job("c2", "valid", "image"))
	s.Save(job("c3", "raw", "text"))
	s.Save(job("c4", "raw", "image"))

	p, _ := s.List(Filter{Status: "valid", MsgType: "image", Limit: 10, Page: 1})
	if p.Total != 1 {
		t.Errorf("valid+image → total=%d, want 1", p.Total)
	}
	if len(p.Jobs) != 1 || p.Jobs[0].ID != "c2" {
		t.Errorf("expected job c2, got %+v", p.Jobs)
	}
}
