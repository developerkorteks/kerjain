package extractor

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// JobPosting is the structured representation of a job vacancy extracted from a message.
type JobPosting struct {
	ID          string `json:"id"`
	SourceGroup string `json:"source_group"`
	GroupName   string `json:"group_name"`
	SenderJID   string `json:"sender_jid"`
	SenderName  string `json:"sender_name"`
	SenderPhone string `json:"sender_phone,omitempty"`
	MsgType     string `json:"msg_type"` // "text" or "image"
	RawText     string `json:"raw_text,omitempty"`
	MediaPath   string `json:"media_path,omitempty"`
	MediaMIME   string `json:"media_mime,omitempty"`
	PostedAt    string `json:"posted_at"`
	ExtractedAt string `json:"extracted_at,omitempty"`
	Status      string `json:"status"` // "raw" | "review" | "valid"

	IsJobPosting bool     `json:"is_job_posting"`
	Title        string   `json:"title,omitempty"`
	Company      string   `json:"company,omitempty"`
	Location     string   `json:"location,omitempty"`
	Gender       string   `json:"gender,omitempty"` // "male", "female", "both"
	AgeMin       int      `json:"age_min,omitempty"`
	AgeMax       int      `json:"age_max,omitempty"`
	Education    string   `json:"education,omitempty"`
	Salary       string   `json:"salary,omitempty"`
	WorkHours    string   `json:"work_hours,omitempty"`
	Contact      string   `json:"contact,omitempty"`
	ContactType  string   `json:"contact_type,omitempty"` // "wa", "email", "langsung"
	Requirements []string `json:"requirements,omitempty"`
	Benefits     []string `json:"benefits,omitempty"`
}

// ── compiled regexes ──

var (
	phoneRe       = regexp.MustCompile(`(?:\+?62[\s\-]?|0)8\d{1,3}[\s\-.]?\d{3,5}[\s\-.]?\d{3,5}[\s\-.]?\d{0,4}`)
	emailRe       = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	ageMaxRe      = regexp.MustCompile(`(?i)(?:usia\s+)?(?:maks?(?:imal)?\.?\s*|max\.?\s*)(\d{2})`)
	ageRangeRe    = regexp.MustCompile(`(?i)usia\s+(\d{2})\s*[-–]\s*(\d{2})`)
	salaryLineRe  = regexp.MustCompile(`(?i)(?:gaji|upah|salary|penghasilan)[^\n]*`)
	hoursLineRe   = regexp.MustCompile(`(?i)(?:jam\s+kerja|working\s+hours?|shift\s+\d)[^\n]*`)
	companyRe     = regexp.MustCompile(`(?i)\b(?:PT|CV|UD|Toko|Kedai|Resto|Warung|Koperasi|Yayasan)[ \t]+[^\n*]{2,50}`)
	locationPhrRe = regexp.MustCompile(`(?i)(?:lokasi|penempatan|area|daerah|wilayah)\s*[:\s]+([^\n]{3,50})`)
	bulletRe      = regexp.MustCompile(`^(?:\d+[.)]\s*|[•\-*✅✓☑❌➡️👉]+\s*)`)
)

// ── keyword lists ──

var jobKeywords = []string{
	"dibutuhkan", "di butuhkan", "dicari", "lowongan", "loker", "hiring",
	"karyawan", "karyawati", "lamaran", "rekrut",
	"kualifikasi", "persyaratan", "berminat", "kirim cv",
	"we're hiring", "membutuhkan", "open rekrut",
	"butuh urgent", "butuh segera", "masih dibutuhkan",
	"tenaga kerja", "pegawai", "staff", "vacancy",
	"penempatan", "join tim", "bergabung",
	"urgently needed", "urgently required", "we need",
	"looking for", "job vacancy", "open position",
}

var jobPrefixes = []string{
	"dibutuhkan segera", "dibutuhkan urgent", "dibutuhkan!",
	"dibutuhkan", "dicari segera", "dicari", "lowongan kerja",
	"loker", "lowongan", "we're hiring", "we are hiring",
	"hiring", "open rekrut", "open recruitment",
	"butuh urgent", "butuh segera", "butuh", "masih dibutuhkan",
	"membutuhkan segera", "membutuhkan",
}

var maleWords   = []string{"laki-laki", "laki laki", "pria", "cowok"}
var femaleWords = []string{"wanita", "perempuan", "cewek", "karyawati"}
var eduLevels   = []string{"s2", "s1", "d4", "d3", "d2", "d1", "smk", "sma/smk", "sma", "smp", "sd"}

var citiesID = []string{
	"semarang", "jakarta", "surabaya", "bandung", "yogyakarta", "solo",
	"medan", "makassar", "tangerang", "bekasi", "depok", "bogor",
	"malang", "palembang", "denpasar", "balikpapan", "samarinda",
}

// ── public API ──

// IsJobPosting returns true if the text contains at least one job posting keyword.
func IsJobPosting(text string) bool {
	lower := strings.ToLower(text)
	for _, kw := range jobKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// Extract returns structured job fields extracted from text.
// All fields come strictly from the input — nothing is invented.
func Extract(text string) *JobPosting {
	j := &JobPosting{IsJobPosting: IsJobPosting(text)}
	if !j.IsJobPosting {
		return j
	}
	lines := toLines(text)
	j.Title = extractTitle(lines)
	j.Company = extractCompany(text)
	j.Location = extractLocation(text)
	j.Gender = extractGender(text)
	j.AgeMin, j.AgeMax = extractAge(text)
	j.Education = extractEducation(text)
	j.Salary = extractSalary(text)
	j.WorkHours = extractWorkHours(text)
	j.Contact, j.ContactType = extractContact(text)
	j.Requirements = extractRequirements(lines)
	j.Benefits = extractBenefits(lines)
	return j
}

// ── field extractors ──

func extractTitle(lines []string) string {
	for _, line := range lines {
		rest := stripJobPrefix(line)
		if rest == "" {
			continue
		}
		clean := cleanText(rest)
		if len(clean) >= 3 && len(clean) <= 80 {
			return titleCase(clean)
		}
	}
	// fallback: first short line whose cleaned form is not a pure noise word
	for _, line := range lines {
		clean := cleanText(line)
		if len(clean) < 3 || len(clean) > 60 || strings.Contains(clean, "@") {
			continue
		}
		cleanLower := strings.ToLower(clean)
		isNoise := false
		for _, p := range jobPrefixes {
			if cleanLower == p {
				isNoise = true
				break
			}
		}
		if !isNoise {
			return titleCase(clean)
		}
	}
	return ""
}

func extractCompany(text string) string {
	m := companyRe.FindString(text)
	if m == "" {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(m), ".,;: ")
}

var locationNoiseRe = regexp.MustCompile(`(?i)^(?:atau|wa|whatsapp|langsung|hub|hubungi|contact|kontak)\b`)

func extractLocation(text string) string {
	if m := locationPhrRe.FindStringSubmatch(text); len(m) > 1 {
		val := strings.TrimSpace(m[1])
		if !locationNoiseRe.MatchString(val) {
			return titleCase(val)
		}
	}
	lower := strings.ToLower(text)
	for _, city := range citiesID {
		if strings.Contains(lower, city) {
			return strings.ToUpper(city[:1]) + city[1:]
		}
	}
	return ""
}

func extractGender(text string) string {
	lower := strings.ToLower(text)
	hasMale := containsAny(lower, maleWords)
	hasFemale := containsAny(lower, femaleWords)
	switch {
	case hasMale && hasFemale:
		return "both"
	case hasMale:
		return "male"
	case hasFemale:
		return "female"
	}
	return ""
}

func extractAge(text string) (int, int) {
	if m := ageRangeRe.FindStringSubmatch(text); len(m) == 3 {
		return atoi(m[1]), atoi(m[2])
	}
	if m := ageMaxRe.FindStringSubmatch(text); len(m) == 2 {
		return 0, atoi(m[1])
	}
	return 0, 0
}

func extractEducation(text string) string {
	lower := strings.ToLower(text)
	for _, edu := range eduLevels {
		if strings.Contains(lower, edu) {
			return strings.ToUpper(edu)
		}
	}
	return ""
}

func extractSalary(text string) string {
	if m := salaryLineRe.FindString(text); m != "" {
		return strings.TrimSpace(m)
	}
	return ""
}

func extractWorkHours(text string) string {
	if m := hoursLineRe.FindString(text); m != "" {
		return strings.TrimSpace(m)
	}
	return ""
}

func extractContact(text string) (string, string) {
	if email := emailRe.FindString(text); email != "" {
		return email, "email"
	}
	if phone := phoneRe.FindString(text); phone != "" {
		norm := normalizePhone(phone)
		if norm != "" {
			return norm, "wa"
		}
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "datang langsung") || strings.Contains(lower, "datang lgsg") ||
		strings.Contains(lower, "walk in") || strings.Contains(lower, "walk-in") {
		return "", "langsung"
	}
	return "", ""
}

func extractRequirements(lines []string) []string {
	var reqs []string
	inSection := false
	reqHeaders := []string{"kualifikasi", "persyaratan", "kriteria", "syarat", "requirement"}
	stopHeaders := []string{"benefit", "fasilitas", "keuntungan", "kirim", "cara melamar", "email ke"}

	for _, line := range lines {
		lower := strings.ToLower(line)
		if containsAny(lower, reqHeaders) {
			inSection = true
			continue
		}
		if inSection && containsAny(lower, stopHeaders) {
			break
		}
		stripped := strings.TrimSpace(bulletRe.ReplaceAllString(line, ""))
		if len(stripped) <= 2 {
			continue
		}
		if inSection || bulletRe.MatchString(line) {
			reqs = append(reqs, stripped)
		}
	}
	return dedup(reqs)
}

func extractBenefits(lines []string) []string {
	var benefits []string
	inSection := false
	benHeaders := []string{"benefit", "fasilitas", "keuntungan", "tunjangan", "kompensasi"}
	stopHeaders := []string{"kualifikasi", "persyaratan", "kriteria", "kirim", "cara melamar"}

	for _, line := range lines {
		lower := strings.ToLower(line)
		if containsAny(lower, benHeaders) {
			inSection = true
			continue
		}
		if inSection && containsAny(lower, stopHeaders) {
			break
		}
		if inSection {
			stripped := strings.TrimSpace(bulletRe.ReplaceAllString(line, ""))
			if len(stripped) > 2 {
				benefits = append(benefits, stripped)
			}
		}
	}
	return benefits
}

// ── helpers ──

func toLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func stripJobPrefix(line string) string {
	lower := strings.ToLower(line)
	for _, p := range jobPrefixes {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSpace(line[len(p):])
		}
	}
	return ""
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.TrimSpace(s)
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		runes := []rune(w)
		if len(runes) > 0 {
			words[i] = strings.ToUpper(string(runes[:1])) + strings.ToLower(string(runes[1:]))
		}
	}
	return strings.Join(words, " ")
}

func containsAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func normalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if unicode.IsDigit(r) || r == '+' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	digits := strings.TrimPrefix(s, "+")
	if len(digits) < 9 || len(digits) > 15 {
		return ""
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func dedup(ss []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
