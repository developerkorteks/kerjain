package api

import (
	"context"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"

	"lokerwa/internal/extractor"
	"lokerwa/internal/hub"
	"lokerwa/internal/storage"
	"lokerwa/internal/whatsapp"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Handler struct {
	manager    *whatsapp.Manager
	hub        *hub.Hub
	store      *storage.Storage
	mu         sync.RWMutex
	lastQR     string
	lastQRPNG  []byte
	ingestKey  string
}

func New(manager *whatsapp.Manager, h *hub.Hub, store *storage.Storage) *Handler {
	handler := &Handler{manager: manager, hub: h, store: store}

	manager.OnQR = func(qrCode string) {
		png, err := qrcode.Encode(qrCode, qrcode.Medium, 256)
		if err != nil {
			return
		}
		handler.mu.Lock()
		handler.lastQR = qrCode
		handler.lastQRPNG = png
		handler.mu.Unlock()
		h.Broadcast("qr", map[string]string{
			"png": base64.StdEncoding.EncodeToString(png),
		})
	}

	manager.OnStateChange = func(s whatsapp.State) {
		if s != whatsapp.StateConnecting {
			handler.mu.Lock()
			handler.lastQR = ""
			handler.lastQRPNG = nil
			handler.mu.Unlock()
		}
		h.Broadcast("status", map[string]string{"state": string(s)})
	}

	manager.OnGroupMsg = func(msg whatsapp.GroupMessage) {
		h.Broadcast("message", msg)
	}

	manager.OnGroupsUpdate = func(groups []whatsapp.GroupInfo) {
		h.Broadcast("groups", groups)
	}

	// Ingest API key: read from env or generate random on startup
	key := os.Getenv("INGEST_API_KEY")
	if key == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		key = hex.EncodeToString(b)
		fmt.Printf("[ingest] No INGEST_API_KEY set — generated ephemeral key: %s\n", key)
	}
	handler.ingestKey = key

	return handler
}

func (h *Handler) Register(r *gin.Engine, mediaDir string) {
	api := r.Group("/api")
	api.GET("/status", h.getStatus)
	api.GET("/qr", h.getQR)
	api.GET("/groups", h.getGroups)
	api.POST("/groups/toggle", h.toggleGroup)
	api.POST("/groups/refresh", h.refreshGroups)
	api.POST("/logout", h.postLogout)
	api.POST("/connect", h.postConnect)
	api.GET("/jobs", h.listJobs)
	api.GET("/jobs/:id", h.getJob)
	api.PATCH("/jobs/:id", h.patchJob)
	api.DELETE("/jobs/:id", h.deleteJob)
	api.POST("/groups/:jid/fetch-history", h.fetchHistory)
	api.POST("/ingest", h.ingestHandler)
	api.POST("/ig/add-session", h.igAddSession)
	api.GET("/ig/config", h.igGetConfig)
	api.POST("/ig/config", h.igSaveConfig)
	api.GET("/ig/status", h.igStatus)

	r.Static("/media", mediaDir)
	r.GET("/ws", h.wsHandler)
}

func (h *Handler) getStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"state":   h.manager.GetState(),
		"clients": h.hub.Count(),
	})
}

func (h *Handler) getQR(c *gin.Context) {
	h.mu.RLock()
	png := h.lastQRPNG
	h.mu.RUnlock()
	if png == nil {
		c.JSON(http.StatusNoContent, nil)
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}

func (h *Handler) postLogout(c *gin.Context) {
	if err := h.manager.Logout(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.mu.Lock()
	h.lastQR = ""
	h.lastQRPNG = nil
	h.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) postConnect(c *gin.Context) {
	if h.manager.IsConnected() {
		c.JSON(http.StatusOK, gin.H{"ok": true, "already": true})
		return
	}
	go func() {
		if err := h.manager.Start(context.Background()); err != nil {
			h.hub.Broadcast("error", map[string]string{"message": err.Error()})
		}
	}()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) getGroups(c *gin.Context) {
	c.JSON(http.StatusOK, h.manager.GetGroups())
}

func (h *Handler) refreshGroups(c *gin.Context) {
	if !h.manager.IsConnected() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not connected"})
		return
	}
	h.manager.RefreshGroups(context.Background())
	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(h.manager.GetGroups())})
}

func (h *Handler) listJobs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// Default: only show is_job_posting=true (public).
	// Pass ?is_job_posting=false → only non-job records.
	// Pass ?is_job_posting=any  → all records, no filter (admin board).
	var isJobPtr *bool
	switch c.Query("is_job_posting") {
	case "false":
		b := false
		isJobPtr = &b
	case "any":
		// nil → buildWhere skips the filter entirely
	default:
		b := true
		isJobPtr = &b
	}

	f := storage.Filter{
		Group:        c.Query("group"),
		MsgType:      c.Query("type"),
		Status:       c.Query("status"),
		Search:       c.Query("q"),
		Sort:         c.Query("sort"),
		DateFrom:     c.Query("date_from"),
		IsJobPosting: isJobPtr,
		Page:         page,
		Limit:        limit,
	}
	if h.store == nil {
		c.JSON(http.StatusOK, storage.Page{})
		return
	}
	result, err := h.store.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) getJob(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	job, err := h.store.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) patchJob(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no store"})
		return
	}
	var patch storage.JobPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.store.UpdateJob(c.Param("id"), patch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	job, err := h.store.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) deleteJob(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err := h.store.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) toggleGroup(c *gin.Context) {
	var body struct {
		JID string `json:"jid"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.JID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "jid required"})
		return
	}
	enabled, err := h.manager.ToggleGroup(body.JID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.hub.Broadcast("groups", h.manager.GetGroups())
	c.JSON(http.StatusOK, gin.H{"jid": body.JID, "enabled": enabled})
}

// fetchHistory requests an on-demand history sync from WhatsApp for a specific group.
// Uses the oldest stored message for the group as the anchor. If none stored, sends a
// no-anchor request (WA will send its default recent batch).
func (h *Handler) fetchHistory(c *gin.Context) {
	if !h.manager.IsConnected() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not connected"})
		return
	}
	jid := c.Param("jid")
	count, _ := strconv.Atoi(c.DefaultQuery("count", "100"))
	if count <= 0 || count > 1000 {
		count = 100
	}

	var msgID, senderJID string
	var fromMe bool
	var ts time.Time

	if h.store != nil {
		oldest, err := h.store.OldestForGroup(jid)
		if err == nil && oldest != nil {
			msgID = oldest.ID
			senderJID = oldest.SenderJID
			ts, _ = time.Parse(time.RFC3339, oldest.PostedAt)
		}
	}

	if err := h.manager.RequestGroupHistory(context.Background(), jid, msgID, senderJID, fromMe, ts, count); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"group":   jid,
		"count":   count,
		"anchor":  msgID,
		"message": "history sync requested — new messages will appear in /api/jobs in ~30s",
	})
}

func (h *Handler) wsHandler(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	// Send current state immediately to this new client
	state := h.manager.GetState()
	_ = conn.WriteJSON(hub.Message{Type: "status", Data: map[string]string{"state": string(state)}})

	// If QR is pending, send it directly so new clients don't miss it
	if state == whatsapp.StateConnecting {
		h.mu.RLock()
		png := h.lastQRPNG
		h.mu.RUnlock()
		if png != nil {
			_ = conn.WriteJSON(hub.Message{Type: "qr", Data: map[string]string{
				"png": base64.StdEncoding.EncodeToString(png),
			}})
		}
	}

	// Send current groups list
	if groups := h.manager.GetGroups(); len(groups) > 0 {
		_ = conn.WriteJSON(hub.Message{Type: "groups", Data: groups})
	}

	h.hub.Register(conn)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// ── ingest endpoint ──────────────────────────────────────────────────────────

type ingestRequest struct {
	Text      string `json:"text"`        // raw post caption / body
	Source    string `json:"source"`      // e.g. "instagram"
	Account   string `json:"account"`     // e.g. "@lokersmg"
	PostedAt  string `json:"posted_at"`   // RFC3339, optional
	MsgID     string `json:"msg_id"`      // unique ID from source
	MediaPath string `json:"media_path"`  // filename inside mediaDir, optional
	MediaMIME string `json:"media_mime"`  // e.g. "image/jpeg"
	APIKey    string `json:"api_key"`
}

func (h *Handler) ingestHandler(c *gin.Context) {
	var req ingestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.APIKey != h.ingestKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api_key"})
		return
	}

	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text required"})
		return
	}

	postedAt := time.Now().UTC()
	if req.PostedAt != "" {
		if t, err := time.Parse(time.RFC3339, req.PostedAt); err == nil {
			postedAt = t
		}
	}

	msgID := req.MsgID
	if msgID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		msgID = req.Source + "_" + hex.EncodeToString(b)
	}

	ext := extractor.Extract(req.Text)
	ext.ID = msgID
	ext.SourceGroup = req.Source + ":" + req.Account
	ext.GroupName = req.Account
	ext.SenderJID = req.Account
	ext.SenderName = req.Account
	if req.MediaPath != "" {
		ext.MsgType = "image"
		ext.MediaPath = req.MediaPath
		ext.MediaMIME = req.MediaMIME
		ext.IsJobPosting = true
	} else {
		ext.MsgType = "text"
	}
	ext.RawText = req.Text
	ext.PostedAt = postedAt.Format(time.RFC3339)

	if err := h.store.Save(ext); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"id":           ext.ID,
		"is_job":       ext.IsJobPosting,
		"title":        ext.Title,
	})
}

// ── IG management endpoints ───────────────────────────────────────────────────

var (
	_igDirOnce sync.Once
	_igDir     string
)

func getIgDir() string {
	_igDirOnce.Do(func() {
		// 0. Explicit override via env (best for production/Docker)
		if env := os.Getenv("IG_SCRAPER_DIR"); env != "" {
			_igDir = env
			return
		}
		// 1. Try next to the running binary
		if ex, err := os.Executable(); err == nil {
			p := filepath.Join(filepath.Dir(filepath.Clean(ex)), "ig_scraper")
			if _, err := os.Stat(p); err == nil {
				_igDir = p
				return
			}
		}
		// 2. Fallback: absolute from CWD
		if abs, err := filepath.Abs("ig_scraper"); err == nil {
			_igDir = abs
			return
		}
		_igDir = "ig_scraper"
	})
	return _igDir
}

func igPythonPath() string {
	dir := getIgDir()
	for _, name := range []string{"python", "python3"} {
		p := filepath.Join(dir, "venv", "bin", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "python3"
}

// POST /api/ig/add-session — accepts cookies JSON array, calls add_session.py --batch
func (h *Handler) igAddSession(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		apiKey = c.Query("api_key")
	}
	if apiKey != h.ingestKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api_key"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil || len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty body"})
		return
	}

	igDir := getIgDir()
	script := filepath.Join(igDir, "add_session.py")
	cmd := exec.CommandContext(c.Request.Context(), igPythonPath(), script, "--batch")
	cmd.Stdin = bytes.NewReader(body)
	cmd.Dir = igDir

	out, err := cmd.Output()
	if err != nil {
		// Try to parse stderr for error message
		var exitErr *exec.ExitError
		if ok := (err.Error() != ""); ok {
			if ee, ok2 := err.(*exec.ExitError); ok2 {
				exitErr = ee
				_ = exitErr
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "detail": string(out)})
		return
	}

	// Forward Python JSON response
	c.Data(http.StatusOK, "application/json", bytes.TrimSpace(out))
}

// GET /api/ig/config
func (h *Handler) igGetConfig(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		apiKey = c.Query("api_key")
	}
	if apiKey != h.ingestKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api_key"})
		return
	}

	data, err := os.ReadFile(filepath.Join(getIgDir(), "config.json"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "config.json not found"})
		return
	}
	c.Data(http.StatusOK, "application/json", data)
}

// POST /api/ig/config
func (h *Handler) igSaveConfig(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		apiKey = c.Query("api_key")
	}
	if apiKey != h.ingestKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api_key"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil || len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty body"})
		return
	}

	// Validate JSON before writing
	var check map[string]any
	if err := json.Unmarshal(body, &check); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}

	if err := os.WriteFile(filepath.Join(getIgDir(), "config.json"), body, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/ig/status — quick file-based + optional live Python check
// ?live=1  → calls check_sessions.py (slower, validates with IG)
func (h *Handler) igStatus(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		apiKey = c.Query("api_key")
	}
	if apiKey != h.ingestKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api_key"})
		return
	}

	if c.Query("live") == "1" {
		// Delegate to Python for live IG validation
		igDir2 := getIgDir()
		script := filepath.Join(igDir2, "check_sessions.py")
		cmd := exec.CommandContext(c.Request.Context(), igPythonPath(), script, "--batch")
		cmd.Dir = igDir2
		out, err := cmd.CombinedOutput()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "detail": string(out)})
			return
		}
		c.Data(http.StatusOK, "application/json", bytes.TrimSpace(out))
		return
	}

	// Fast path: read config + check file existence / age only
	data, err := os.ReadFile(filepath.Join(getIgDir(), "config.json"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "config.json not found"})
		return
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type accountStatus struct {
		Username    string  `json:"username"`
		Enabled     bool    `json:"enabled"`
		FileExists  bool    `json:"file_exists"`
		FileAgeDays float64 `json:"file_age_days"`
		Status      string  `json:"status"`
		Message     string  `json:"message"`
	}

	accounts, _ := cfg["scraper_accounts"].([]any)
	result := make([]accountStatus, 0, len(accounts))

	for _, raw := range accounts {
		acc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		username, _ := acc["username"].(string)
		sessionFile, _ := acc["session_file"].(string)
		enabled, _ := acc["enabled"].(bool)
		if _, hasEnabled := acc["enabled"]; !hasEnabled {
			enabled = true
		}

		st := accountStatus{Username: username, Enabled: enabled}

		fpath := filepath.Join(getIgDir(), sessionFile)
		info, err := os.Stat(fpath)
		if err != nil {
			st.Status = "missing"
			st.Message = "Session file tidak ada. Silakan login ulang."
		} else {
			st.FileExists = true
			age := time.Since(info.ModTime()).Hours() / 24
			st.FileAgeDays = math.Round(age*10) / 10
			if age > 30 {
				st.Status = "expired"
				st.Message = fmt.Sprintf("Session berumur %.0f hari, kemungkinan expired.", age)
			} else {
				st.Status = "ok"
				st.Message = fmt.Sprintf("Session aktif (diperbarui %.1f hari lalu)", age)
			}
		}
		result = append(result, st)
	}

	c.JSON(http.StatusOK, result)
}
