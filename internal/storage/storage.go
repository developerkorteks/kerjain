package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"lokerwa/internal/extractor"
)

// Storage manages persistence of job postings in SQLite.
type Storage struct {
	db *sql.DB
}

// Filter for listing job postings.
type Filter struct {
	Group        string
	MsgType      string // "text" or "image"
	Status       string // "raw", "review", "valid"
	Search       string // free-text search against title, company, raw_text
	Role         string // category slug: admin, staff, sales, kitchen, barista, produksi, tutor, freelance
	Sort         string // "newest" (default) | "oldest"
	DateFrom     string // ISO date string e.g. "2026-05-10" — filter posted_at >= this
	IsJobPosting *bool
	Page         int
	Limit        int
}

// JobPatch carries editable fields for PATCH /api/jobs/:id.
type JobPatch struct {
	Status       string   `json:"status"`
	IsJobPosting bool     `json:"is_job_posting"`
	Title        string   `json:"title"`
	Company      string   `json:"company"`
	Location     string   `json:"location"`
	Gender       string   `json:"gender"`
	AgeMin       int      `json:"age_min"`
	AgeMax       int      `json:"age_max"`
	Education    string   `json:"education"`
	Salary       string   `json:"salary"`
	WorkHours    string   `json:"work_hours"`
	Contact      string   `json:"contact"`
	ContactType  string   `json:"contact_type"`
	Requirements []string `json:"requirements"`
	Benefits     []string `json:"benefits"`
}

// Page is a paginated result.
type Page struct {
	Jobs  []extractor.JobPosting `json:"jobs"`
	Total int                    `json:"total"`
	Page  int                    `json:"page"`
	Limit int                    `json:"limit"`
}

type StatusTotals struct {
	Raw    int `json:"raw"`
	Review int `json:"review"`
	Valid  int `json:"valid"`
}

type GroupSummary struct {
	Group     string `json:"group"`
	GroupName string `json:"group_name"`
	Raw       int    `json:"raw"`
	Review    int    `json:"review"`
	Valid     int    `json:"valid"`
	Total     int    `json:"total"`
}

type Summary struct {
	Totals StatusTotals   `json:"totals"`
	Groups []GroupSummary `json:"groups"`
}

// New opens (or creates) the SQLite database and runs migrations.
func New(dbPath string) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("storage mkdir: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(on)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("storage open: %w", err)
	}
	s := &Storage{db: db}
	return s, s.migrate()
}

func (s *Storage) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS job_postings (
		id           TEXT PRIMARY KEY,
		source_group TEXT NOT NULL,
		group_name   TEXT NOT NULL DEFAULT '',
		sender_jid   TEXT NOT NULL,
		sender_name  TEXT NOT NULL DEFAULT '',
		sender_phone TEXT NOT NULL DEFAULT '',
		msg_type     TEXT NOT NULL DEFAULT 'text',
		raw_text     TEXT,
		media_path   TEXT,
		media_mime   TEXT,
		posted_at    TEXT NOT NULL,
		extracted_at TEXT NOT NULL,
		is_job_posting INTEGER NOT NULL DEFAULT 1,
		title        TEXT,
		company      TEXT,
		location     TEXT,
		gender       TEXT,
		age_min      INTEGER DEFAULT 0,
		age_max      INTEGER DEFAULT 0,
		education    TEXT,
		salary       TEXT,
		work_hours   TEXT,
		contact      TEXT,
		contact_type TEXT,
		requirements TEXT DEFAULT '[]',
		benefits     TEXT DEFAULT '[]',
		status       TEXT NOT NULL DEFAULT 'raw'
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_group  ON job_postings(source_group);
	CREATE INDEX IF NOT EXISTS idx_jobs_posted ON job_postings(posted_at DESC);
	CREATE INDEX IF NOT EXISTS idx_jobs_type   ON job_postings(msg_type);
	`)
	if err != nil {
		return err
	}
	// Add status column to existing databases (no-op if already present).
	_, _ = s.db.Exec(`ALTER TABLE job_postings ADD COLUMN status TEXT DEFAULT 'raw'`)
	// Backfill rows that have NULL status.
	_, _ = s.db.Exec(`UPDATE job_postings SET status='raw' WHERE status IS NULL`)
	// Create status index after column is guaranteed to exist.
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_status ON job_postings(status)`)
	// Add sender_phone column to existing databases.
	_, _ = s.db.Exec(`ALTER TABLE job_postings ADD COLUMN sender_phone TEXT NOT NULL DEFAULT ''`)
	// Search index for fast free-text queries across title/company/location/raw_text.
	_, err = s.db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS job_postings_fts USING fts5(
		id UNINDEXED,
		title,
		company,
		location,
		raw_text,
		tokenize='unicode61'
	);
	`)
	if err != nil {
		return err
	}
	return s.rebuildSearchIndex()
}

// Save inserts a job posting. Duplicate IDs are silently ignored.
func (s *Storage) Save(job *extractor.JobPosting) error {
	reqs, _ := json.Marshal(job.Requirements)
	bens, _ := json.Marshal(job.Benefits)
	if job.ExtractedAt == "" {
		job.ExtractedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if string(reqs) == "null" {
		reqs = []byte("[]")
	}
	if string(bens) == "null" {
		bens = []byte("[]")
	}
	status := job.Status
	if status == "" {
		status = "raw"
	}
	res, err := s.db.Exec(`
	INSERT OR IGNORE INTO job_postings
	  (id, source_group, group_name, sender_jid, sender_name, sender_phone, msg_type,
	   raw_text, media_path, media_mime, posted_at, extracted_at,
	   is_job_posting, title, company, location, gender, age_min, age_max,
	   education, salary, work_hours, contact, contact_type, requirements, benefits, status)
	VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		job.ID, job.SourceGroup, job.GroupName, job.SenderJID, job.SenderName, job.SenderPhone,
		job.MsgType, job.RawText, job.MediaPath, job.MediaMIME,
		job.PostedAt, job.ExtractedAt,
		boolToInt(job.IsJobPosting),
		nullStr(job.Title), nullStr(job.Company), nullStr(job.Location),
		nullStr(job.Gender), job.AgeMin, job.AgeMax,
		nullStr(job.Education), nullStr(job.Salary), nullStr(job.WorkHours),
		nullStr(job.Contact), nullStr(job.ContactType),
		string(reqs), string(bens), status,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil
	}
	return s.upsertSearchDoc(job)
}

// List returns a paginated list of job postings filtered by the given criteria.
func (s *Storage) List(f Filter) (*Page, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	offset := (f.Page - 1) * f.Limit

	where, args := buildWhere(f)

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM job_postings"+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	order := "DESC"
	if f.Sort == "oldest" {
		order = "ASC"
	}
	queryArgs := append(args, f.Limit, offset)
	rows, err := s.db.Query(
		"SELECT "+jobColumns+" FROM job_postings"+where+" ORDER BY posted_at "+order+" LIMIT ? OFFSET ?",
		queryArgs...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, err
	}
	return &Page{Jobs: jobs, Total: total, Page: f.Page, Limit: f.Limit}, nil
}

// GetByID returns a single job posting by ID.
func (s *Storage) GetByID(id string) (*extractor.JobPosting, error) {
	rows, err := s.db.Query("SELECT "+jobColumns+" FROM job_postings WHERE id = ? LIMIT 1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return &jobs[0], nil
}

func (s *Storage) Summary(f Filter) (*Summary, error) {
	where, args := buildWhere(summaryFilter(f))

	rows, err := s.db.Query(`
		SELECT source_group, group_name,
		       SUM(CASE WHEN status='raw' THEN 1 ELSE 0 END) AS raw_count,
		       SUM(CASE WHEN status='review' THEN 1 ELSE 0 END) AS review_count,
		       SUM(CASE WHEN status='valid' THEN 1 ELSE 0 END) AS valid_count,
		       COUNT(*) AS total_count
		FROM job_postings`+where+`
		GROUP BY source_group, group_name
		ORDER BY raw_count DESC, total_count DESC, group_name ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out Summary
	for rows.Next() {
		var g GroupSummary
		if err := rows.Scan(&g.Group, &g.GroupName, &g.Raw, &g.Review, &g.Valid, &g.Total); err != nil {
			return nil, err
		}
		out.Groups = append(out.Groups, g)
		out.Totals.Raw += g.Raw
		out.Totals.Review += g.Review
		out.Totals.Valid += g.Valid
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &out, nil
}

// OldestForGroup returns the oldest job posting for a given group JID, used as
// the anchor when requesting on-demand history sync from WhatsApp.
func (s *Storage) OldestForGroup(groupJID string) (*extractor.JobPosting, error) {
	rows, err := s.db.Query("SELECT "+jobColumns+" FROM job_postings WHERE source_group = ? ORDER BY posted_at ASC LIMIT 1", groupJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	return &jobs[0], nil
}

// Delete removes a job posting by ID.
func (s *Storage) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM job_postings WHERE id = ?", id)
	if err != nil {
		return err
	}
	return s.deleteSearchDoc(id)
}

// UpdateJob applies a partial patch to an existing job posting.
func (s *Storage) UpdateJob(id string, p JobPatch) error {
	reqs, _ := json.Marshal(p.Requirements)
	bens, _ := json.Marshal(p.Benefits)
	if string(reqs) == "null" {
		reqs = []byte("[]")
	}
	if string(bens) == "null" {
		bens = []byte("[]")
	}
	status := p.Status
	if status == "" {
		status = "raw"
	}
	_, err := s.db.Exec(`
	UPDATE job_postings SET
		status=?, is_job_posting=?, title=?, company=?, location=?,
		gender=?, age_min=?, age_max=?, education=?, salary=?,
		work_hours=?, contact=?, contact_type=?, requirements=?, benefits=?
	WHERE id=?`,
		status, boolToInt(p.IsJobPosting),
		nullStr(p.Title), nullStr(p.Company), nullStr(p.Location),
		nullStr(p.Gender), p.AgeMin, p.AgeMax,
		nullStr(p.Education), nullStr(p.Salary), nullStr(p.WorkHours),
		nullStr(p.Contact), nullStr(p.ContactType),
		string(reqs), string(bens), id,
	)
	if err != nil {
		return err
	}
	job, err := s.GetByID(id)
	if err != nil {
		return err
	}
	return s.upsertSearchDoc(job)
}

// jobColumns is the explicit SELECT column list to guarantee scan order.
const jobColumns = `id, source_group, group_name, sender_jid, sender_name, sender_phone,
	msg_type, raw_text, media_path, media_mime, posted_at, extracted_at,
	is_job_posting, title, company, location, gender, age_min, age_max,
	education, salary, work_hours, contact, contact_type, requirements, benefits, status`

// ── internal ──

func buildWhere(f Filter) (string, []interface{}) {
	var conds []string
	var args []interface{}

	if f.Group != "" {
		conds = append(conds, "source_group = ?")
		args = append(args, f.Group)
	}
	if f.MsgType != "" {
		conds = append(conds, "msg_type = ?")
		args = append(args, f.MsgType)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if f.IsJobPosting != nil {
		conds = append(conds, "is_job_posting = ?")
		args = append(args, boolToInt(*f.IsJobPosting))
	}
	if f.Search != "" {
		if expr := buildSearchExpr(f.Search); expr != "" {
			conds = append(conds, "id IN (SELECT id FROM job_postings_fts WHERE job_postings_fts MATCH ?)")
			args = append(args, expr)
		}
	}
	if roleWhere, roleArgs := buildRoleWhere(f.Role); roleWhere != "" {
		conds = append(conds, roleWhere)
		args = append(args, roleArgs...)
	}
	if f.DateFrom != "" {
		conds = append(conds, "posted_at >= ?")
		args = append(args, f.DateFrom)
	}

	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func summaryFilter(f Filter) Filter {
	f.Page = 0
	f.Limit = 0
	f.Sort = ""
	f.Status = ""
	f.Group = ""
	return f
}

func scanJobs(rows *sql.Rows) ([]extractor.JobPosting, error) {
	var jobs []extractor.JobPosting
	for rows.Next() {
		var j extractor.JobPosting
		var isJobInt int
		var reqs, bens string
		var title, company, location, gender sql.NullString
		var education, salary, workHours, contact, contactType sql.NullString
		var rawText, mediaPath, mediaMIME sql.NullString

		err := rows.Scan(
			&j.ID, &j.SourceGroup, &j.GroupName, &j.SenderJID, &j.SenderName, &j.SenderPhone,
			&j.MsgType, &rawText, &mediaPath, &mediaMIME,
			&j.PostedAt, &j.ExtractedAt, &isJobInt,
			&title, &company, &location, &gender,
			&j.AgeMin, &j.AgeMax,
			&education, &salary, &workHours, &contact, &contactType,
			&reqs, &bens, &j.Status,
		)
		if err != nil {
			return nil, err
		}
		j.IsJobPosting = isJobInt == 1
		j.RawText = rawText.String
		j.MediaPath = mediaPath.String
		j.MediaMIME = mediaMIME.String
		j.Title = title.String
		j.Company = company.String
		j.Location = location.String
		j.Gender = gender.String
		j.Education = education.String
		j.Salary = salary.String
		j.WorkHours = workHours.String
		j.Contact = contact.String
		j.ContactType = contactType.String
		_ = json.Unmarshal([]byte(reqs), &j.Requirements)
		_ = json.Unmarshal([]byte(bens), &j.Benefits)
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (s *Storage) rebuildSearchIndex() error {
	if _, err := s.db.Exec(`DELETE FROM job_postings_fts`); err != nil {
		return err
	}
	_, err := s.db.Exec(`
	INSERT INTO job_postings_fts (id, title, company, location, raw_text)
	SELECT id, COALESCE(title, ''), COALESCE(company, ''), COALESCE(location, ''), COALESCE(raw_text, '')
	FROM job_postings
	`)
	return err
}

func (s *Storage) upsertSearchDoc(job *extractor.JobPosting) error {
	if err := s.deleteSearchDoc(job.ID); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO job_postings_fts (id, title, company, location, raw_text) VALUES (?,?,?,?,?)`,
		job.ID, job.Title, job.Company, job.Location, job.RawText,
	)
	return err
}

func (s *Storage) deleteSearchDoc(id string) error {
	_, err := s.db.Exec(`DELETE FROM job_postings_fts WHERE id = ?`, id)
	return err
}

func buildSearchExpr(input string) string {
	terms := splitSearchTerms(input)
	if len(terms) == 0 {
		return ""
	}
	for i := range terms {
		terms[i] = `"` + strings.ReplaceAll(terms[i], `"`, `""`) + `"*`
	}
	return strings.Join(terms, " AND ")
}

func buildRoleWhere(role string) (string, []interface{}) {
	patterns := rolePatterns(strings.ToLower(strings.TrimSpace(role)))
	if len(patterns) == 0 {
		return "", nil
	}
	cols := []string{"title", "raw_text"}
	parts := make([]string, 0, len(patterns)*len(cols))
	args := make([]interface{}, 0, len(patterns)*len(cols))
	for _, p := range patterns {
		like := "%" + p + "%"
		for _, col := range cols {
			parts = append(parts, col+" LIKE ?")
			args = append(args, like)
		}
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func rolePatterns(role string) []string {
	switch role {
	case "admin":
		return []string{"admin", "administrasi"}
	case "staff":
		return []string{"staff", "crew", "karyawan", "pegawai"}
	case "sales":
		return []string{"sales", "marketing", "customer service", "account officer", "spg", "spb"}
	case "kitchen":
		return []string{"cook", "chef", "kitchen", "dapur", "waiter", "waitress", "server"}
	case "barista":
		return []string{"barista", "kopi", "coffee", "f&b", "outlet"}
	case "produksi":
		return []string{"produksi", "gudang", "warehouse", "operator", "sorter", "packer"}
	case "tutor":
		return []string{"tutor", "guru", "pengajar", "teacher"}
	case "freelance":
		return []string{"freelance", "daily worker", "daily-worker"}
	case "parttime":
		return []string{"part time", "part-time", "parttime"}
	default:
		return nil
	}
}

func splitSearchTerms(input string) []string {
	raw := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(input)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	terms := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, term := range raw {
		if len(term) < 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}
