package app

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LemonPG/115driver/pkg/driver"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type viewModel struct {
	Config      Config
	LoggedIn    bool
	LoginType   string
	LastScanAt  string
	LastScanMsg string
	LastErr     string
	Running     bool
}

func (a *App) renderTemplate(c *gin.Context, name string, data any) {
	tfs, _ := fs.Sub(webFS, "web")
	tpl := template.Must(template.ParseFS(tfs, "index.html"))
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(c.Writer, "index.html", data); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
	}
}

func (a *App) handleIndex(c *gin.Context) {
	if c.Request.URL.Path != "/" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	a.mu.Lock()
	vm := viewModel{
		Config:      a.cfg,
		LoggedIn:    a.cfg.Credential != nil,
		LoginType:   a.cfg.LoginType,
		LastScanAt:  a.lastScanAt.Format(time.RFC3339),
		LastScanMsg: a.lastScanMsg,
		LastErr:     a.lastErr,
		Running:     a.scanCancel != nil,
	}
	a.mu.Unlock()

	a.renderTemplate(c, "index.html", vm)
}

func (a *App) handleConfig(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		a.mu.Lock()
		cfg := a.cfg
		a.mu.Unlock()
		c.JSON(http.StatusOK, cfg)
		return
	case http.MethodPost:
		var patch struct {
			UrlPrefix          *string   `json:"urlPrefix"`
			OutputDir          *string   `json:"outputDir"`
			ScanInterval       *int      `json:"scanIntervalMinutes"`
			SelectedCID        *string   `json:"selectedCid"`
			PendingCID         *string   `json:"pendingCid"`
			ExistingCID        *string   `json:"existingCid"`
			RedundantCID       *string   `json:"redundantCid"`
			MaxDepth           *int      `json:"maxDepth"`
			RateLimit          *float64  `json:"rateLimit"`
			ScanExtensions     *[]string `json:"scanExtensions"`
			DownloadExtensions *[]string `json:"downloadExtensions"`
		}
		if err := c.ShouldBindJSON(&patch); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		a.mu.Lock()
		if patch.UrlPrefix != nil {
			a.cfg.UrlPrefix = strings.TrimRight(*patch.UrlPrefix, "/")
		}
		if patch.OutputDir != nil {
			a.cfg.OutputDir = *patch.OutputDir
		}
		if patch.ScanInterval != nil && *patch.ScanInterval > 0 {
			a.cfg.ScanInterval = *patch.ScanInterval
		}
		if patch.SelectedCID != nil {
			a.cfg.SelectedCID = *patch.SelectedCID
		}
		if patch.PendingCID != nil {
			a.cfg.PendingCID = *patch.PendingCID
		}
		if patch.ExistingCID != nil {
			a.cfg.ExistingCID = *patch.ExistingCID
		}
		if patch.RedundantCID != nil {
			a.cfg.RedundantCID = *patch.RedundantCID
		}
		if patch.MaxDepth != nil && *patch.MaxDepth >= 0 {
			a.cfg.MaxDepth = *patch.MaxDepth
		}
		if patch.RateLimit != nil && *patch.RateLimit >= 0 {
			a.cfg.RateLimit = *patch.RateLimit
			// 重新初始化限流器
			if a.cfg.RateLimit > 0 {
				// burst参数设置为1，表示允许突发1个请求
				a.limiter = rate.NewLimiter(rate.Limit(a.cfg.RateLimit), 1)
			} else {
				a.limiter = nil // no limit
			}
		}
		if patch.ScanExtensions != nil {
			a.cfg.ScanExtensions = *patch.ScanExtensions
		}
		if patch.DownloadExtensions != nil {
			a.cfg.DownloadExtensions = *patch.DownloadExtensions
		}
		a.client = nil // force re-login check next time
		err := a.saveConfigLocked()
		a.mu.Unlock()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	default:
		c.AbortWithStatus(http.StatusMethodNotAllowed)
	}
}

type qrState struct {
	Session   *driver.QRCodeSession `json:"session,omitempty"`
	PNGBase64 string                `json:"pngBase64,omitempty"`
	Status    *driver.QRCodeStatus  `json:"status,omitempty"`
}

func (a *App) handleQRCodeStart(c *gin.Context) {
	s, err := a.rateLimitedQRCodeStart()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	png, err := s.QRCode()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// store session in memory? For simplicity we return to client and let client keep it.
	c.JSON(http.StatusOK, qrState{Session: s, PNGBase64: toBase64(png)})
}

func (a *App) handleQRCodeStatus(c *gin.Context) {
	var body struct {
		Session driver.QRCodeSession `json:"session"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	st, err := a.rateLimitedQRCodeStatus(&body.Session)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, qrState{Status: st})
}

func (a *App) handleQRCodeLogin(c *gin.Context) {
	var body struct {
		Session driver.QRCodeSession `json:"session"`
		App     string               `json:"app"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	st, err := a.rateLimitedQRCodeStatus(&body.Session)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !st.IsAllowed() {
		c.JSON(http.StatusOK, gin.H{"ok": false, "status": st})
		return
	}

	// choose app
	loginApp := driver.LoginAppWeb
	if body.App != "" {
		switch strings.ToLower(body.App) {
		case "android":
			loginApp = driver.LoginAppAndroid
		case "ios":
			loginApp = driver.LoginAppIOS
		case "tv":
			loginApp = driver.LoginAppTV
		case "alipaymini":
			loginApp = driver.LoginAppAlipayMini
		case "wechatmini":
			loginApp = driver.LoginAppWechatMini
		case "web":
			loginApp = driver.LoginAppWeb
		}
	}

	cr, err := a.rateLimitedQRCodeLoginWithApp(&body.Session, loginApp)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	a.mu.Lock()
	a.cfg.Credential = cr
	// Save login type
	a.cfg.LoginType = string(loginApp)
	a.client = nil
	err = a.saveConfigLocked()
	a.mu.Unlock()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type dirEntry struct {
	CID   string `json:"cid"`
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

func (a *App) handleDirList(c *gin.Context) {
	cid := c.Query("cid")
	if cid == "" {
		cid = "0"
	}

	// 解析分页参数
	offsetStr := c.Query("offset")
	limitStr := c.Query("limit")

	var offset, limit int64
	var err error

	if offsetStr != "" {
		offset, err = strconv.ParseInt(offsetStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid offset parameter"})
			return
		}
		if offset < 0 {
			offset = 0
		}
	}

	if limitStr != "" {
		limit, err = strconv.ParseInt(limitStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
			return
		}
		if limit < 1 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}
	} else {
		limit = 100 // 默认每页100条
	}

	a.mu.Lock()
	client, err := a.ensureClientLocked()
	a.mu.Unlock()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	entries := make([]dirEntry, 0)

	// 使用分页获取文件列表
	var files *[]driver.File
	var paths *[]driver.PathInfo

	// 使用分页 API（带限流）
	files, paths, err = a.rateLimitedListPage(client, cid, offset, limit)

	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// if cid != "0" && len(*paths) > 0 {
	if cid != "0" && len(*paths) > 0 {
		entries = append(entries, dirEntry{CID: (*paths)[len(*paths)-1].ParentID, Name: "..", IsDir: true})
	}
	// 过滤出目录
	for _, f := range *files {
		if !f.IsDirectory {
			continue
		}
		entries = append(entries, dirEntry{CID: f.FileID, Name: f.Name, IsDir: true})
	}
	c.JSON(http.StatusOK, entries)
}

func (a *App) handleScanOnce(c *gin.Context) {
	created, updated, skipped, err := a.scanOnce()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"created": created, "updated": updated, "skipped": skipped})
}

func (a *App) handleScanIncremental(c *gin.Context) {
	created, updated, skipped, err := a.scanIncremental()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"created": created, "updated": updated, "skipped": skipped})
}

// handleUpdateDirectory 更新指定目录及其子目录中的变动文件和文件夹
func (a *App) handleUpdateDirectory(c *gin.Context) {
	var req struct {
		CID string `json:"cid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.CID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cid is required"})
		return
	}

	created, updated, skipped, err := a.updateDirectory(req.CID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"created": created,
		"updated": updated,
		"skipped": skipped,
		"message": fmt.Sprintf("Directory update completed: created=%d updated=%d skipped=%d", created, updated, skipped),
	})
}

func (a *App) handleScanStart(c *gin.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scanCancel != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "running": true})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.scanCancel = cancel
	interval := time.Duration(a.cfg.ScanInterval) * time.Minute
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _, _, _ = a.scanOnce()
			}
		}
	}()
	c.JSON(http.StatusOK, gin.H{"ok": true, "running": true})
}

func (a *App) handleScanStop(c *gin.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scanCancel != nil {
		a.scanCancel()
		a.scanCancel = nil
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "running": false})
}

func (a *App) handleDownload(c *gin.Context) {
	// 从 URL 路径中提取 pickcode
	// 路径格式: /d/{pickcode}
	path := strings.TrimPrefix(c.Request.URL.Path, "/d/")
	if path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing pickcode"})
		return
	}

	// 去除可能的前导斜杠
	path = strings.TrimPrefix(path, "/")
	pickCode := path

	a.mu.Lock()
	client, err := a.ensureClientLocked()
	a.mu.Unlock()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	info, err := a.rateLimitedDownload2Redirect(client, pickCode, LinkArgs{
		IP:       c.Request.Host,
		Header:   c.Request.Header,
		Type:     c.Query("type"),
		Redirect: true,
	})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if info.Url.Url == "" {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no download url available"})
		return
	}

	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate")

	// 返回 302 重定向到直链地址
	c.Redirect(http.StatusFound, info.Url.Url)
}

// handleDatabaseFiles returns all file states from database
func (a *App) handleDatabaseFiles(c *gin.Context) {
	if a.fileRepo == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "database not available"})
		return
	}

	files, err := a.fileRepo.GetAll()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"files": files,
		"total": len(files),
	})
}

// handleDatabaseHistory returns scan history from database
func (a *App) handleDatabaseHistory(c *gin.Context) {
	if a.scanRepo == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "database not available"})
		return
	}

	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 10
	}

	histories, err := a.scanRepo.GetRecent(limit)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"histories": histories,
		"total":     len(histories),
	})
}

// handleDatabaseDeleteFile deletes a file state from database
func (a *App) handleDatabaseDeleteFile(c *gin.Context) {
	if a.fileRepo == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "database not available"})
		return
	}

	fileID := c.Param("fileId")
	if fileID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "fileId is required"})
		return
	}

	if err := a.fileRepo.DeleteByFileID(fileID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "file deleted successfully"})
}

// handleLogin handles user login
func (a *App) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := a.userRepo.GetByUsername(req.Username)
	if err != nil {
		// 用户不存在或数据库错误
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	// 检查是否已设置密码
	if !user.IsSetup || user.PasswordHash == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "password not set, please set password first"})
		return
	}

	// 验证密码
	if !CheckPasswordHash(req.Password, user.PasswordHash) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	// 生成安全的会话令牌
	token, err := GenerateSecureToken(32) // 32字节 = 64字符十六进制
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to generate session token"})
		return
	}

	// 创建会话记录（存储在内存中）
	session := &Session{
		UserID:       user.ID,
		Token:        token,
		ExpiresAt:    time.Now().Add(7 * 24 * time.Hour), // 7天有效期
		LastActivity: time.Now(),
		UserAgent:    c.Request.UserAgent(),
		IPAddress:    c.ClientIP(),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := a.sessionStore.Create(session); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	// 设置cookie
	c.SetCookie("FilmFlow_session", token, 3600*24*7, "/", "", false, true) // 7天有效期，HttpOnly

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "login successful",
		"user": gin.H{
			"username": user.Username,
			"isSetup":  user.IsSetup,
		},
		"session": gin.H{
			"token":     token,
			"expiresAt": session.ExpiresAt,
		},
	})
}

// handleLogout handles user logout
func (a *App) handleLogout(c *gin.Context) {
	// 获取cookie中的会话令牌
	if cookie, err := c.Cookie("FilmFlow_session"); err == nil && cookie != "" {
		// 从内存存储中删除会话
		a.sessionStore.DeleteByToken(cookie)

		// 清除cookie
		c.SetCookie("FilmFlow_session", "", -1, "/", "", false, true)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "logout successful",
	})
}

// handleSetPassword handles password setup (first run)
func (a *App) handleSetPassword(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := a.userRepo.GetByUsername(req.Username)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "user not found"})
		return
	}

	// 检查是否已经设置过密码
	if user.IsSetup {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "password already set"})
		return
	}

	// 哈希密码
	hashedPassword, err := HashPassword(req.Password)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	// 更新用户
	user.PasswordHash = hashedPassword
	user.IsSetup = true
	user.UpdatedAt = time.Now()

	if err := a.userRepo.Update(user); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "password set successfully",
	})
}

// handleCheckAuth checks if user is authenticated and if password is set
func (a *App) handleCheckAuth(c *gin.Context) {
	user, err := a.userRepo.GetFirst()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"authenticated": false,
			"isSetup":       false,
			"message":       "no user found",
		})
		return
	}

	// 检查用户是否已设置密码
	if !user.IsSetup {
		c.JSON(http.StatusOK, gin.H{
			"authenticated": false,
			"isSetup":       false,
			"username":      user.Username,
			"message":       "password not set",
		})
		return
	}

	// 检查认证状态 - 使用内存会话存储
	isAuthenticated := false
	var session *Session

	// 首先检查cookie
	if cookie, err := c.Cookie("FilmFlow_session"); err == nil && cookie != "" {
		session, err = a.sessionStore.GetByToken(cookie)
		if err == nil && session != nil {
			// 检查会话是否过期
			if session.ExpiresAt.After(time.Now()) {
				isAuthenticated = true
				// 更新最后活动时间
				a.sessionStore.UpdateLastActivity(session.ID)
			} else {
				// 删除过期会话
				a.sessionStore.DeleteByToken(cookie)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"authenticated": isAuthenticated,
		"isSetup":       user.IsSetup,
		"username":      user.Username,
	})
}

// authMiddleware is a middleware to protect routes that require authentication
func (a *App) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查用户是否已设置密码
		user, err := a.userRepo.GetFirst()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to get user"})
			return
		}

		// 如果用户未设置密码，允许访问（首次运行需要设置密码）
		if !user.IsSetup {
			c.Next()
			return
		}

		// 检查cookie
		if cookie, err := c.Cookie("FilmFlow_session"); err == nil && cookie != "" {
			session, err := a.sessionStore.GetByToken(cookie)
			if err == nil && session != nil {
				// 检查会话是否过期
				if session.ExpiresAt.After(time.Now()) {
					// 更新最后活动时间
					a.sessionStore.UpdateLastActivity(session.ID)
					c.Next()
					return
				} else {
					// 删除过期会话
					a.sessionStore.DeleteByToken(cookie)
				}
			}
		}

		// 也支持旧的认证方式（向后兼容）
		authHeader := c.GetHeader("Authorization")
		if authHeader == "Bearer authenticated" {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
	}
}

// handleScanAndProcess handles scanning and processing pending directory
func (a *App) handleScanAndProcess(c *gin.Context) {
	if a.pendingScanner == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "pending scanner not initialized"})
		return
	}

	// 执行扫描和处理
	go func() {
		a.pendingScanner.scanAndProcess()
	}()

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "pending directory scan and process started",
	})
}
