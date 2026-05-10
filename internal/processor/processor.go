package processor

import (
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lokerwa/internal/extractor"
	"lokerwa/internal/storage"
	"lokerwa/internal/whatsapp"
)

// Processor handles incoming messages and extracts job postings.
type Processor struct {
	store    *storage.Storage
	mediaDir string
}

// New creates a Processor. mediaDir is where downloaded images are saved.
func New(store *storage.Storage, mediaDir string) (*Processor, error) {
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return nil, err
	}
	return &Processor{store: store, mediaDir: mediaDir}, nil
}

// Handle receives a GroupMessage and routes it through the pipeline.
func (p *Processor) Handle(msg whatsapp.GroupMessage) {
	log.Printf("[proc] %s | type=%-8s | group=%s | body=%.60s",
		msg.MsgID, msg.MsgType, msg.GroupName, msg.Body+msg.Caption)
	switch msg.MsgType {
	case "image":
		go p.processImage(msg)
	case "text":
		go p.processText(msg)
	default:
		log.Printf("[proc] SKIP type=%s", msg.MsgType)
	}
}

// ── text pipeline ──

func phoneFromJID(jid string) string {
	if idx := strings.Index(jid, "@"); idx > 0 {
		return jid[:idx]
	}
	return jid
}

func (p *Processor) processText(msg whatsapp.GroupMessage) {
	text := strings.TrimSpace(msg.Body)
	if text == "" {
		return
	}

	ext := extractor.Extract(text)
	ext.ID = msg.MsgID
	ext.SourceGroup = msg.GroupJID
	ext.GroupName = msg.GroupName
	ext.SenderJID = msg.SenderJID
	ext.SenderName = msg.SenderName
	ext.SenderPhone = phoneFromJID(msg.SenderJID)
	ext.MsgType = "text"
	ext.RawText = text
	ext.PostedAt = msg.Timestamp.Format(time.RFC3339)

	if err := p.store.Save(ext); err != nil {
		log.Printf("[proc] save text ERROR: %v", err)
	} else {
		log.Printf("[proc] saved text id=%s is_job=%v title=%q", ext.ID, ext.IsJobPosting, ext.Title)
	}
}

// ── image pipeline ──

func (p *Processor) processImage(msg whatsapp.GroupMessage) {
	job := &extractor.JobPosting{
		ID:           msg.MsgID,
		SourceGroup:  msg.GroupJID,
		GroupName:    msg.GroupName,
		SenderJID:    msg.SenderJID,
		SenderName:   msg.SenderName,
		SenderPhone:  phoneFromJID(msg.SenderJID),
		MsgType:      "image",
		RawText:      msg.Caption,
		IsJobPosting: true,
		PostedAt:     msg.Timestamp.Format(time.RFC3339),
	}

	// Extract from caption if available
	if msg.Caption != "" {
		extracted := extractor.Extract(msg.Caption)
		job.Title = extracted.Title
		job.Company = extracted.Company
		job.Location = extracted.Location
		job.Contact = extracted.Contact
		job.ContactType = extracted.ContactType
		job.Gender = extracted.Gender
		job.AgeMin = extracted.AgeMin
		job.AgeMax = extracted.AgeMax
		job.Education = extracted.Education
		job.Salary = extracted.Salary
		job.Requirements = extracted.Requirements
		job.Benefits = extracted.Benefits
		if !extracted.IsJobPosting {
			job.IsJobPosting = true // images in job groups are assumed to be job postings
		}
	}

	// Download and save image
	if msg.DownloadFn != nil {
		data, mimeType, err := msg.DownloadFn()
		if err != nil {
			log.Printf("[processor] download image %s: %v", msg.MsgID, err)
		} else if len(data) > 0 {
			ext := extFromMIME(mimeType)
			filename := msg.MsgID + ext
			savePath := filepath.Join(p.mediaDir, filename)
			if writeErr := os.WriteFile(savePath, data, 0644); writeErr == nil {
				job.MediaPath = filename
				job.MediaMIME = mimeType
			} else {
				log.Printf("[processor] write image %s: %v", filename, writeErr)
			}
		}
	}

	if err := p.store.Save(job); err != nil {
		log.Printf("[processor] save image error: %v", err)
	}
}

// ── helpers ──

func extFromMIME(mimeType string) string {
	exts, _ := mime.ExtensionsByType(mimeType)
	if len(exts) > 0 {
		// prefer .jpg over .jpeg for image/jpeg
		for _, e := range exts {
			if e == ".jpg" || e == ".png" || e == ".webp" || e == ".gif" {
				return e
			}
		}
		return exts[0]
	}
	// fallback by common types
	switch strings.Split(mimeType, "/")[1] {
	case "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	case "webp":
		return ".webp"
	case "gif":
		return ".gif"
	}
	return ".bin"
}
