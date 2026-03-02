package app

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LemonPG/115driver/pkg/driver"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

//go:embed web/*
var webFS embed.FS

type LinkArgs struct {
	IP       string
	Header   http.Header
	Type     string
	Redirect bool
}
type App struct {
	mu sync.Mutex

	cfgPath string
	cfg     Config

	client *driver.Pan115Client

	// TMDB client for movie/tv show information
	tmdbClient *TMDBClient

	// rate limiter for all client requests
	limiter *rate.Limiter

	// database
	database     *Database
	fileRepo     *FileStateRepository
	scanRepo     *ScanHistoryRepository
	taskRepo     *TaskQueueRepository
	userRepo     *UserRepository
	behaviorRepo *BehaviorRecordRepository
	sessionStore *MemorySessionStore

	// runtime status
	lastScanAt  time.Time
	lastScanMsg string
	lastErr     string

	// scheduler
	scanCancel context.CancelFunc

	// behavior monitor
	behaviorMonitorCancel context.CancelFunc

	// route manager for Emby redirect rules
	routeManager *RouteManager

	// pending directory scanner
	pendingScanner *PendingScanner
}

func Run(cfgPath string) error {
	if cfgPath == "" {
		cfgPath = "config.json"
	}
	app := &App{cfgPath: cfgPath, cfg: defaultConfig()}
	if err := app.loadConfig(); err != nil {
		log.Printf("load config: %v", err)
	}

	// Initialize route manager
	app.routeManager = NewRouteManager(&app.cfg.EmbyRedirect)

	// Initialize database
	if err := app.initDatabase(); err != nil {
		log.Printf("init database: %v", err)
		return err
	}

	// Initialize TMDB client if API key is configured
	if app.cfg.TMDB.APIKey != "" {
		app.tmdbClient = NewTMDBClient(app.cfg.TMDB.APIKey)
		log.Printf("TMDB client initialized with API key")
	} else {
		log.Printf("TMDB API key not configured, TMDB features will be disabled")
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	app.registerRoutes(router)

	// Start main server
	log.Printf("FilmFlow listen on http://%s", app.cfg.ListenAddr)

	// Start Emby proxy server if enabled
	if app.cfg.EmbyProxy.Enabled && app.cfg.EmbyProxy.Target != "" && app.cfg.EmbyProxy.ProxyPort != "" {
		go app.startProxyServer()
	}

	// Start automatic scheduled scanning if scan interval is greater than 0
	if app.cfg.ScanInterval > 0 {
		//app.startAutoScan()
		log.Printf("Auto scan started with interval: %d minutes", app.cfg.ScanInterval)
	} else {
		log.Printf("Auto scan disabled (scan interval: %d minutes)", app.cfg.ScanInterval)
	}

	// Start behavior monitoring
	app.startBehaviorMonitor()
	log.Printf("Behavior monitoring started")

	// Initialize and start pending directory scanner if configured
	app.pendingScanner = NewPendingScanner(app)
	if app.cfg.PendingCID != "" && app.cfg.ScanInterval > 0 {
		// Start pending directory auto scan with the same interval as main scan
		interval := time.Duration(app.cfg.ScanInterval) * time.Minute
		app.pendingScanner.StartAutoScan(interval)
		log.Printf("Pending directory auto scan started with interval: %v", interval)
	} else {
		log.Printf("Pending directory auto scan disabled (PendingCID: %s, ScanInterval: %d)",
			app.cfg.PendingCID, app.cfg.ScanInterval)
	}

	return router.Run(app.cfg.ListenAddr)
}

func (a *App) initDatabase() error {
	db, err := NewDatabase(a.cfg.Database.DataDir)
	if err != nil {
		return err
	}

	a.database = db
	a.fileRepo = NewFileStateRepository(db.DB)
	a.scanRepo = NewScanHistoryRepository(db.DB)
	a.taskRepo = NewTaskQueueRepository(db.DB)
	a.userRepo = NewUserRepository(db.DB)
	a.behaviorRepo = NewBehaviorRecordRepository(db.DB)
	a.sessionStore = NewMemorySessionStore()

	// 启动会话清理任务（每小时清理一次过期会话）
	a.sessionStore.Cleanup(1 * time.Hour)

	// 检查是否需要创建默认用户
	if err := a.ensureDefaultUser(); err != nil {
		log.Printf("Failed to ensure default user: %v", err)
	}

	return nil
}

func (a *App) ensureClientLocked() (*driver.Pan115Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	if a.cfg.Credential == nil {
		return nil, fmt.Errorf("not logged in")
	}
	c := driver.Default().SetDebug(false).ImportCredential(a.cfg.Credential)
	if err := c.LoginCheck(); err != nil {
		return nil, err
	}
	a.client = c
	return c, nil
}

func (a *App) registerRoutes(router *gin.Engine) {
	// 公开路由（不需要认证）
	router.GET("/", a.handleIndex)
	router.GET("/api/auth/check", a.handleCheckAuth)
	router.POST("/api/auth/login", a.handleLogin)
	router.POST("/api/auth/logout", a.handleLogout) // 注销路由
	router.POST("/api/auth/setup", a.handleSetPassword)
	router.GET("/d/*pickcode", a.handleDownload)

	// 需要认证的API路由组
	api := router.Group("/api")
	api.Use(a.authMiddleware())
	{
		api.GET("/config", a.handleConfig)
		api.POST("/config", a.handleConfig)
		api.POST("/qrcode/start", a.handleQRCodeStart)
		api.POST("/qrcode/status", a.handleQRCodeStatus)
		api.POST("/qrcode/login", a.handleQRCodeLogin)
		api.GET("/dir/list", a.handleDirList)
		api.POST("/scan/once", a.handleScanOnce)
		api.POST("/scan/incremental", a.handleScanIncremental)
		api.POST("/scan/start", a.handleScanStart)
		api.POST("/scan/stop", a.handleScanStop)
		api.POST("/scan/process", a.handleScanAndProcess)
		api.POST("/directory/update", a.handleUpdateDirectory)

		// Database API routes
		api.GET("/database/files", a.handleDatabaseFiles)
		api.GET("/database/history", a.handleDatabaseHistory)
		api.DELETE("/database/files/:fileId", a.handleDatabaseDeleteFile)

		// TMDB API routes
		api.GET("/tmdb/config", a.handleTMDBConfig)
		api.POST("/tmdb/config", a.handleTMDBConfig)
		api.POST("/tmdb/search/movies", a.handleSearchMovies)
		api.POST("/tmdb/search/tv", a.handleSearchTV)
		api.GET("/tmdb/movie/:movieId", a.handleGetMovieDetails)
		api.GET("/tmdb/tv/:tvId", a.handleGetTVDetails)
		api.GET("/tmdb/search/movies/query", a.handleSearchMoviesWithQuery)
		api.GET("/tmdb/search/tv/query", a.handleSearchTVWithQuery)
		api.GET("/tmdb/image/format", a.handleFormatImageURL)
	}

	// static
	sub, _ := fs.Sub(webFS, "web")
	router.StaticFS("/static", http.FS(sub))
}

// updateDirectory 递归更新指定目录及其子目录中的变动文件和文件夹
// 基于文件的 UpdateTime 检测变动，只更新发生变化的文件和文件夹
func (a *App) updateDirectory(cid string) (created, updated, skipped int, err error) {
	startTime := time.Now()

	a.mu.Lock()
	cfg := a.cfg
	c, cerr := a.ensureClientLocked()
	a.mu.Unlock()
	if cerr != nil {
		a.setLastErr(cerr)
		return 0, 0, 0, cerr
	}
	if cfg.OutputDir == "" {
		err := fmt.Errorf("outputDir is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}
	if cfg.UrlPrefix == "" {
		err := fmt.Errorf("urlPrefix is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	if cid == "" {
		err := fmt.Errorf("cid is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	// 创建扫描历史记录
	scanHistory := &ScanHistory{
		ScanTime: startTime,
		RootCID:  cid,
		MaxDepth: cfg.MaxDepth,
		Status:   "running",
		Message:  fmt.Sprintf("Directory update started for CID: %s", cid),
	}
	if err := a.scanRepo.Create(scanHistory); err != nil {
		log.Printf("Failed to create scan history: %v", err)
	}

	// 获取目录信息
	dirInfo, err := a.rateLimitedStat(c, cid)
	if err != nil {
		a.setLastErr(err)
		scanHistory.Status = "failed"
		scanHistory.Message = fmt.Sprintf("Failed to get directory info: %v", err)
		a.scanRepo.Update(scanHistory)
		return 0, 0, 0, fmt.Errorf("failed to get directory info for CID %s: %v", cid, err)
	}

	// 检查目录是否存在数据库中
	existingDir, err := a.fileRepo.GetByFileID(cid)
	dirRelPath := ""
	if err == nil && existingDir.IsDir {
		// 目录已存在，使用数据库中的路径
		dirRelPath = existingDir.FilePath
	} else {
		// 目录不存在于数据库中，需要获取其完整路径
		// 这里简化处理，使用目录名作为相对路径
		dirRelPath = sanitizeFileName(dirInfo.Name)
	}

	// 递归扫描目录
	created, updated, skipped, err = a.scanCIDRecursiveWithUpdateCheck(c, cfg, dirInfo.Parents[len(dirInfo.Parents)-1].ID, cid, dirRelPath, 0, true)
	if err != nil {
		a.setLastErr(err)
		scanHistory.Status = "failed"
		scanHistory.Message = err.Error()
		a.scanRepo.Update(scanHistory)
		return created, updated, skipped, err
	}

	// 更新扫描历史
	duration := time.Since(startTime)
	scanHistory.Created = created
	scanHistory.Updated = updated
	scanHistory.Skipped = skipped
	scanHistory.Total = created + updated + skipped
	scanHistory.Duration = duration.String()
	scanHistory.Status = "success"
	scanHistory.Message = fmt.Sprintf("Directory update completed: created=%d updated=%d skipped=%d", created, updated, skipped)
	a.scanRepo.Update(scanHistory)

	a.mu.Lock()
	a.lastScanAt = time.Now()
	a.lastScanMsg = fmt.Sprintf("directory update: created=%d updated=%d skipped=%d", created, updated, skipped)
	a.lastErr = ""
	a.mu.Unlock()

	return created, updated, skipped, nil
}

// scanCIDRecursiveWithUpdateCheck 递归扫描目录，基于 UpdateTime 检测变动
func (a *App) scanCIDRecursiveWithUpdateCheck(c *driver.Pan115Client, cfg Config, parentCID string, cid string, relPath string, depth int, checkFolderNeedsUpdate bool) (created, updated, skipped int, err error) {
	// 获取目录中的文件列表
	files, err := a.rateLimitedList(c, cid)
	if err != nil {
		return 0, 0, 0, err
	}

	// 检查文件夹是否需要更新
	existingFolder, err := a.fileRepo.GetByFileID(cid)
	folderNeedsUpdate := false
	if err == nil && existingFolder.IsDir {
		if checkFolderNeedsUpdate {
			folderNeedsUpdate = true
		} else {
			// 获取文件夹的最新信息
			folderInfo, err := a.rateLimitedStat(c, cid)
			if err == nil {
				//比较更新时间
				if folderInfo.UpdateTime.After(existingFolder.UpdateTime) {
					folderNeedsUpdate = true
					log.Printf("Folder needs update: %s (CID: %s), old update time: %v, new update time: %v",
						existingFolder.FileName, cid, existingFolder.UpdateTime, folderInfo.UpdateTime)
				}
			}
		}
	}

	// 更新文件夹信息
	folderName := "Root"
	if relPath != "" {
		parts := strings.Split(relPath, "/")
		if len(parts) > 0 {
			folderName = parts[len(parts)-1]
		}
	}

	// 获取文件夹的最新信息以获取正确的创建和更新时间
	folderInfo, err := a.rateLimitedStat(c, cid)
	folderCreateTime := time.Time{}
	folderUpdateTime := time.Time{}
	if err == nil {
		folderCreateTime = folderInfo.CreateTime
		folderUpdateTime = folderInfo.UpdateTime
	}

	folderState := &FileState{
		FileID:        cid,
		PickCode:      "", // 文件夹可能没有pickcode
		FileName:      folderName,
		FilePath:      relPath,
		StrmPath:      "",
		URL:           "",
		Size:          0,
		IsDir:         true,
		ParentFileID:  parentCID, // 设置父文件夹ID
		Depth:         depth,
		FolderPath:    relPath,
		LastScannedAt: time.Now(),
		CreateTime:    folderCreateTime,
		UpdateTime:    folderUpdateTime,
	}

	if err := a.fileRepo.Upsert(folderState); err != nil {
		log.Printf("Failed to upsert folder state: %v", err)
	}

	// 如果文件夹需要更新，或者我们还没有扫描过这个文件夹，那么需要处理其内容
	if folderNeedsUpdate || err != nil {
		// 处理文件夹中的文件和子目录
		return a.processDirectoryContents(c, cfg, cid, relPath, depth, files, checkFolderNeedsUpdate)
	}

	// 文件夹没有变化，跳过处理
	skipped++
	log.Printf("Folder unchanged, skipping: %s (CID: %s)", folderName, cid)
	return 0, 0, 1, nil
}

// processDirectoryContents 处理目录中的文件和子目录
func (a *App) processDirectoryContents(c *driver.Pan115Client, cfg Config, cid string, relPath string, depth int, files *[]driver.File, checkFolderNeedsUpdate bool) (created, updated, skipped int, err error) {
	// 确保输出目录存在
	outDir := filepath.Join(cfg.OutputDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, 0, 0, err
	}

	// 确保下载目录存在
	downloadOutDir := filepath.Join(cfg.OutputDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(downloadOutDir, 0o755); err != nil {
		return 0, 0, 0, err
	}

	// 查询数据库中该目录下的所有文件和子目录
	dbFiles, err := a.fileRepo.GetByParentFileID(cid)
	if err != nil {
		log.Printf("Failed to get files from database for parent CID %s: %v", cid, err)
		// 继续处理，不中断整个流程
	}

	// 创建115网盘文件ID的Map，用于快速查找
	cloudFileIDs := make(map[string]bool)
	for _, f := range *files {
		cloudFileIDs[f.FileID] = true
	}

	// 统计文件名以避免冲突
	nameCount := map[string]int{}
	for _, f := range *files {
		if f.IsDirectory {
			continue
		}
		if f.PickCode == "" {
			continue
		}
		// 检查文件是否应该被扫描
		if !hasExtension(f.Name, cfg.ScanExtensions) && !hasExtension(f.Name, cfg.DownloadExtensions) {
			continue
		}
		base := sanitizeFileName(f.Name)
		nameCount[base]++
	}

	// 处理每个文件/文件夹
	for _, f := range *files {
		if f.IsDirectory {
			if depth >= cfg.MaxDepth {
				continue
			}

			// 创建子目录路径
			childRel := relPath
			if childRel != "" {
				childRel += "/"
			}
			childRel += sanitizeFileName(f.Name)

			// 检查子目录是否存在，如果不存在则创建
			childOutDir := filepath.Join(cfg.OutputDir, filepath.FromSlash(childRel))
			if _, err := os.Stat(childOutDir); os.IsNotExist(err) {
				if err := os.MkdirAll(childOutDir, 0o755); err != nil {
					log.Printf("Failed to create directory %s: %v", childOutDir, err)
					// 继续处理，不中断整个流程
				} else {
					log.Printf("Created directory: %s", childOutDir)
				}
			}

			// 递归处理子目录
			cCreated, cUpdated, cSkipped, cErr := a.scanCIDRecursiveWithUpdateCheck(c, cfg, cid, f.FileID, childRel, depth+1, checkFolderNeedsUpdate)
			created += cCreated
			updated += cUpdated
			skipped += cSkipped
			if cErr != nil {
				return created, updated, skipped, cErr
			}
			continue
		}

		// 处理文件
		if f.PickCode == "" {
			skipped++
			continue
		}

		// 检查文件是否应该被扫描
		if !hasExtension(f.Name, cfg.ScanExtensions) && !hasExtension(f.Name, cfg.DownloadExtensions) {
			skipped++
			continue
		}

		// 检查文件是否需要更新
		existingFile, err := a.fileRepo.GetByFileID(f.FileID)
		fileNeedsUpdate := false
		if err == nil && !existingFile.IsDir {
			if checkFolderNeedsUpdate {
				fileNeedsUpdate = true
			} else {
				// 比较更新时间
				if f.UpdateTime.After(existingFile.UpdateTime) {
					fileNeedsUpdate = true
					log.Printf("File needs update: %s (CID: %s), old update time: %v, new update time: %v",
						existingFile.FileName, f.FileID, existingFile.UpdateTime, f.UpdateTime)
				}
			}
		}

		// 如果文件不存在于数据库中或者需要更新，则处理它
		if err != nil || fileNeedsUpdate {
			// 处理文件
			cCreated, cUpdated, cSkipped, cErr := a.processFile(c, cfg, cid, relPath, depth, f, nameCount)
			created += cCreated
			updated += cUpdated
			skipped += cSkipped
			if cErr != nil {
				return created, updated, skipped, cErr
			}
		} else {
			// 文件没有变化，跳过
			skipped++
			log.Printf("File unchanged, skipping: %s (CID: %s)", f.Name, f.FileID)
		}
	}

	// 处理删除：删除数据库中存在但115网盘中不存在的文件和目录
	deleted := 0
	log.Printf("Checking for deletions in directory CID: %s, database records: %d, cloud files: %d",
		cid, len(dbFiles), len(*files))

	for _, dbFile := range dbFiles {
		if !cloudFileIDs[dbFile.FileID] {
			// 文件或目录在115网盘中不存在，需要删除
			log.Printf("Found file/directory to delete: %s (CID: %s, IsDir: %v)",
				dbFile.FileName, dbFile.FileID, dbFile.IsDir)
			if err := a.deleteFileOrDirectory(&dbFile); err != nil {
				log.Printf("Failed to delete file/directory %s (CID: %s): %v", dbFile.FileName, dbFile.FileID, err)
			} else {
				deleted++
				log.Printf("Successfully deleted file/directory: %s (CID: %s)", dbFile.FileName, dbFile.FileID)
			}
		} else {
			log.Printf("File/directory exists in cloud: %s (CID: %s)", dbFile.FileName, dbFile.FileID)
		}
	}

	if deleted > 0 {
		log.Printf("Total deleted in directory CID %s: %d", cid, deleted)
	}

	// 注意：这里不返回deleted计数，因为现有的函数签名只返回created, updated, skipped
	// 如果需要统计删除数量，需要修改函数签名
	return created, updated, skipped, nil
}

// deleteFileOrDirectory 删除文件或目录（包括数据库记录和本地文件）
func (a *App) deleteFileOrDirectory(fileState *FileState) error {
	// 删除数据库记录
	if err := a.fileRepo.DeleteByFileID(fileState.FileID); err != nil {
		return fmt.Errorf("failed to delete database record for %s (CID: %s): %v", fileState.FileName, fileState.FileID, err)
	}

	// 如果是目录，需要递归删除其所有子文件和子目录
	if fileState.IsDir {
		// 查询该目录下的所有文件和子目录
		childFiles, err := a.fileRepo.GetByParentFileID(fileState.FileID)
		if err != nil {
			log.Printf("Failed to get child files for directory %s (CID: %s): %v", fileState.FileName, fileState.FileID, err)
			// 继续删除本地文件，不中断
		}

		// 递归删除子文件和子目录
		for _, childFile := range childFiles {
			if err := a.deleteFileOrDirectory(&childFile); err != nil {
				log.Printf("Failed to delete child file/directory %s (CID: %s): %v", childFile.FileName, childFile.FileID, err)
				// 继续处理其他文件，不中断
			}
		}
	}

	// 删除本地文件
	if fileState.StrmPath != "" {
		// 删除.strm文件或下载的文件
		if _, err := os.Stat(fileState.StrmPath); err == nil {
			if err := os.Remove(fileState.StrmPath); err != nil {
				return fmt.Errorf("failed to delete local file %s: %v", fileState.StrmPath, err)
			}
			log.Printf("Deleted local file: %s", fileState.StrmPath)
		}
	}

	// 如果是目录，还需要删除本地目录（如果为空）
	if fileState.IsDir {
		// 构建本地目录路径
		localDir := filepath.Join(a.cfg.OutputDir, filepath.FromSlash(fileState.FilePath))
		if _, err := os.Stat(localDir); err == nil {
			// 尝试删除目录（如果为空）
			if err := os.Remove(localDir); err != nil {
				// 目录可能不为空，这是正常的，我们只记录日志
				log.Printf("Directory not empty or cannot be removed: %s (error: %v)", localDir, err)
			} else {
				log.Printf("Deleted local directory: %s", localDir)
			}
		}
	}

	return nil
}

// processFile 处理单个文件
func (a *App) processFile(c *driver.Pan115Client, cfg Config, parentCID string, relPath string, depth int, f driver.File, nameCount map[string]int) (created, updated, skipped int, err error) {
	base := sanitizeFileName(f.Name)
	baseWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))

	if nameCount[baseWithoutExt] > 1 {
		baseWithoutExt = fmt.Sprintf("%s__%s", baseWithoutExt, f.FileID)
	}

	// 检查文件是否应该直接下载
	if shouldDownload(f.Name, cfg.DownloadExtensions) {
		// 下载文件
		downloadPath := filepath.Join(cfg.OutputDir, filepath.FromSlash(relPath), base)
		downloadPath = filepath.ToSlash(downloadPath)

		// 下载文件
		if err := a.downloadFile(c, f.PickCode, f.Name, downloadPath); err != nil {
			log.Printf("Failed to download file %s: %v", f.Name, err)
			return 0, 0, 1, nil
		}

		// 创建/更新文件状态
		fileState := &FileState{
			FileID:        f.FileID,
			PickCode:      f.PickCode,
			FileName:      f.Name,
			FilePath:      relPath,
			StrmPath:      downloadPath,
			URL:           "", // 下载文件没有URL
			Size:          f.Size,
			IsDir:         false,
			ParentFileID:  parentCID,
			Depth:         depth,
			FolderPath:    relPath,
			LastScannedAt: time.Now(),
			CreateTime:    f.CreateTime,
			UpdateTime:    f.UpdateTime,
		}

		if err := a.fileRepo.Upsert(fileState); err != nil {
			log.Printf("Failed to upsert file state: %v", err)
		}

		return 1, 0, 0, nil
	}

	// 为非下载文件生成 .strm 文件
	outPath := filepath.Join(cfg.OutputDir, filepath.FromSlash(relPath), baseWithoutExt+".strm")
	outPath = filepath.ToSlash(outPath)
	url := cfg.UrlPrefix + "/d/" + f.PickCode + "?" + f.Name

	// 检查文件是否已经存在
	existingFile, err := a.fileRepo.GetByFileID(f.FileID)
	isUpdate := false
	shouldCreateFile := true

	if err == nil && existingFile.PickCode == f.PickCode {
		isUpdate = true
		// 检查本地文件是否存在
		if _, statErr := os.Stat(outPath); statErr == nil {
			// 本地文件存在，检查内容是否相同
			if b, rerr := os.ReadFile(outPath); rerr == nil {
				if strings.TrimSpace(string(b)) == url {
					// 文件内容和URL都相同，跳过创建
					shouldCreateFile = false
				}
			}
		}
		// 如果本地文件不存在，shouldCreateFile保持为true，会重新创建文件
	}

	// 创建/更新文件状态
	fileState := &FileState{
		FileID:        f.FileID,
		PickCode:      f.PickCode,
		FileName:      f.Name,
		FilePath:      relPath,
		StrmPath:      outPath,
		URL:           url,
		Size:          f.Size,
		IsDir:         false,
		ParentFileID:  parentCID,
		Depth:         depth,
		FolderPath:    relPath,
		LastScannedAt: time.Now(),
		CreateTime:    f.CreateTime,
		UpdateTime:    f.UpdateTime,
	}

	if err := a.fileRepo.Upsert(fileState); err != nil {
		log.Printf("Failed to upsert file state: %v", err)
	}

	// 如果需要创建文件
	if shouldCreateFile {
		// 检查文件目录是否存在，如果不存在则创建
		fileDir := filepath.Dir(outPath)
		if _, err := os.Stat(fileDir); os.IsNotExist(err) {
			if err := os.MkdirAll(fileDir, 0o755); err != nil {
				log.Printf("Failed to create directory %s: %v", fileDir, err)
				return 0, 0, 0, err
			}
			log.Printf("Created directory for file: %s", fileDir)
		}

		// 写入 .strm 文件
		if err := os.WriteFile(outPath, []byte(url+"\n"), 0o644); err != nil {
			return 0, 0, 0, err
		}

		if isUpdate {
			return 0, 1, 0, nil
		}
		return 1, 0, 0, nil
	}

	// 文件已经存在且内容相同，跳过
	return 0, 0, 1, nil
}

// scanIncremental performs an incremental scan, skipping already scanned folders
func (a *App) scanIncremental() (created, updated, skipped int, err error) {
	startTime := time.Now()

	a.mu.Lock()
	cfg := a.cfg
	c, cerr := a.ensureClientLocked()
	a.mu.Unlock()
	if cerr != nil {
		a.setLastErr(cerr)
		return 0, 0, 0, cerr
	}
	if cfg.OutputDir == "" {
		err := fmt.Errorf("outputDir is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}
	if cfg.UrlPrefix == "" {
		err := fmt.Errorf("urlPrefix is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	if cfg.SelectedCID == "" {
		err := fmt.Errorf("selectedCid is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	// Create scan history record for incremental scan
	scanHistory := &ScanHistory{
		ScanTime: startTime,
		RootCID:  cfg.SelectedCID,
		MaxDepth: cfg.MaxDepth,
		Status:   "running",
		Message:  "Incremental scan started",
	}
	if err := a.scanRepo.Create(scanHistory); err != nil {
		log.Printf("Failed to create scan history: %v", err)
	}

	created, updated, skipped, err = a.scanCIDRecursive(c, cfg, cfg.SelectedCID, "", 0, true) // true表示增量扫描
	if err != nil {
		a.setLastErr(err)
		scanHistory.Status = "failed"
		scanHistory.Message = err.Error()
		a.scanRepo.Update(scanHistory)
		return created, updated, skipped, err
	}

	// Update scan history
	duration := time.Since(startTime)
	scanHistory.Created = created
	scanHistory.Updated = updated
	scanHistory.Skipped = skipped
	scanHistory.Total = created + updated + skipped
	scanHistory.Duration = duration.String()
	scanHistory.Status = "success"
	scanHistory.Message = "Incremental scan completed successfully"
	a.scanRepo.Update(scanHistory)

	a.mu.Lock()
	a.lastScanAt = time.Now()
	a.lastScanMsg = fmt.Sprintf("incremental: created=%d updated=%d skipped=%d", created, updated, skipped)
	a.lastErr = ""
	a.mu.Unlock()

	return created, updated, skipped, nil
}

// monitorBehaviorAndRescan 监控行为详情并重新扫描受影响的目录
// 监控的操作类型：new_folder, copy_folder, folder_rename, move_file, delete_file
func (a *App) monitorBehaviorAndRescan() error {
	a.mu.Lock()
	client, err := a.ensureClientLocked()
	cfg := a.cfg
	a.mu.Unlock()
	if err != nil {
		return fmt.Errorf("无法获取客户端: %v", err)
	}

	// 定义需要监控的操作类型
	targetTypes := []string{
		"new_folder",    // 新增目录
		"copy_folder",   // 复制目录
		"folder_rename", // 目录改名
		"move_file",     // 移动文件或目录
		"delete_file",   // 删除文件或目录
	}

	// 用于存储需要重新扫描的目录ID
	directoriesToRescan := make(map[string]bool)

	// 检查每种操作类型
	for _, operationType := range targetTypes {

		// 获取今天的行为详情
		resp, err := a.rateLimitedBehaviorDetailWithApp(client, cfg.LoginType, map[string]interface{}{
			"type":   operationType,
			"limit":  100, // 获取较多的记录以提高检测概率
			"offset": 0,
			"date":   time.Now().Format("2006-01-02"),
		})

		if err != nil {
			log.Printf("获取行为详情失败 (类型: %s): %v", operationType, err)
			continue
		}

		// 处理每条行为记录
		for _, behavior := range resp.Data.List {
			// 检查该行为记录是否已经处理过
			exists, err := a.behaviorRepo.Exists(behavior.ID)
			if err != nil {
				log.Printf("检查行为记录是否存在失败 (ID=%s): %v", behavior.ID, err)
				continue
			}

			if exists {
				// 已经处理过，跳过
				continue
			}

			// 记录该行为记录到数据库
			behaviorRecord := &BehaviorRecord{
				BehaviorID:    behavior.ID,
				OperationType: operationType,
				CID:           a.extractCIDFromBehavior(behavior, operationType),
				ProcessedAt:   time.Now(),
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}

			if err := a.behaviorRepo.Create(behaviorRecord); err != nil {
				log.Printf("记录行为记录到数据库失败 (ID=%s): %v", behavior.ID, err)
			}

			cid := behaviorRecord.CID
			if cid != "" {
				// 检查该目录是否在扫描库内
				if a.isDirectoryInScanLibrary(cid) {
					directoriesToRescan[cid] = true
					log.Printf("检测到需要重新扫描的目录: CID=%s, 操作类型=%s, 行为ID=%s", cid, operationType, behavior.ID)
				}
			}
		}
	}

	// 重新扫描受影响的目录
	for cid := range directoriesToRescan {
		log.Printf("开始重新扫描目录: CID=%s", cid)

		// 获取目录的相对路径
		relPath, err := a.getDirectoryRelativePath(cid)
		if err != nil {
			log.Printf("获取目录路径失败 (CID=%s): %v", cid, err)
			continue
		}

		// 重新扫描该目录
		a.mu.Lock()
		cfg := a.cfg
		a.mu.Unlock()

		// 使用增量扫描重新扫描该目录
		_, _, _, err = a.scanCIDRecursive(client, cfg, cid, relPath, 0, true)
		if err != nil {
			log.Printf("重新扫描目录失败 (CID=%s): %v", cid, err)
		} else {
			log.Printf("目录重新扫描完成: CID=%s", cid)
		}
	}

	if len(directoriesToRescan) > 0 {
		log.Printf("行为监控完成，共重新扫描 %d 个目录", len(directoriesToRescan))
	}

	return nil
}

// extractCIDFromBehavior 从行为详情中提取目录ID
func (a *App) extractCIDFromBehavior(behavior driver.BehaviorDetailItem, operationType string) string {
	if behavior.FileID != "" {
		return behavior.FileID
	}
	return behavior.ParentID
	// 对于不同的操作类型，CID可能来自不同的字段：
	/*switch operationType {
	case "new_folder", "copy_folder", "folder_rename":
		// 对于文件夹操作，CID可能来自FileID（新创建的文件夹）或ParentID（父文件夹）
		// 优先使用FileID，如果FileID为空则使用ParentID
		if behavior.FileID != "" {
			return behavior.FileID
		}
		return behavior.ParentID
	case "move_file", "delete_file":
		// 对于文件操作，CID可能来自ParentID（文件所在的文件夹）
		return behavior.ParentID
	default:
		// 默认情况下，尝试使用FileID或ParentID
		if behavior.FileID != "" {
			return behavior.FileID
		}
		return behavior.ParentID
	}*/
}

// extractCIDFromJSON 从JSON数据中提取CID，支持多种JSON结构
func extractCIDFromJSON(data interface{}, operationType string) string {
	// 处理map类型
	if m, ok := data.(map[string]interface{}); ok {
		// 首先检查常见的CID字段
		if cid, ok := m["cid"].(string); ok && cid != "" {
			return cid
		}
		if cid, ok := m["category_id"].(string); ok && cid != "" {
			return cid
		}
		if cid, ok := m["file_id"].(string); ok && cid != "" {
			return cid
		}
		if cid, ok := m["id"].(string); ok && cid != "" {
			// 检查是否是有效的CID（通常是数字字符串）
			// 这里可以根据需要添加更严格的验证
			return cid
		}

		// 检查嵌套结构：data -> list -> file_id
		if dataField, ok := m["data"].(map[string]interface{}); ok {
			if list, ok := dataField["list"].([]interface{}); ok && len(list) > 0 {
				// 取第一个元素
				if firstItem, ok := list[0].(map[string]interface{}); ok {
					if cid, ok := firstItem["file_id"].(string); ok && cid != "" {
						return cid
					}
					if cid, ok := firstItem["cid"].(string); ok && cid != "" {
						return cid
					}
					if cid, ok := firstItem["category_id"].(string); ok && cid != "" {
						return cid
					}
					if cid, ok := firstItem["id"].(string); ok && cid != "" {
						return cid
					}
				}
			}

			// 检查data字段中是否有直接的file_id
			if cid, ok := dataField["file_id"].(string); ok && cid != "" {
				return cid
			}
		}

		// 根据操作类型检查特定字段
		switch operationType {
		case "new_folder", "copy_folder", "folder_rename":
			// 对于文件夹操作，检查folder_id等字段
			if cid, ok := m["folder_id"].(string); ok && cid != "" {
				return cid
			}
		case "move_file", "delete_file":
			// 对于文件操作，检查file_id等字段
			if cid, ok := m["file_id"].(string); ok && cid != "" {
				return cid
			}
		}
	}

	// 处理数组类型
	if arr, ok := data.([]interface{}); ok && len(arr) > 0 {
		// 递归处理第一个元素
		return extractCIDFromJSON(arr[0], operationType)
	}

	return ""
}

// isDirectoryInScanLibrary 检查目录是否在扫描库内
func (a *App) isDirectoryInScanLibrary(cid string) bool {
	// 检查数据库中是否存在该目录的记录
	fileState, err := a.fileRepo.GetByFileID(cid)
	if err != nil {
		return false
	}

	// 如果是目录且在扫描库内
	return fileState.IsDir
}

// getDirectoryRelativePath 获取目录的相对路径
func (a *App) getDirectoryRelativePath(cid string) (string, error) {
	// 从数据库中获取目录信息
	fileState, err := a.fileRepo.GetByFileID(cid)
	if err != nil {
		return "", err
	}

	// 返回目录的相对路径
	return fileState.FilePath, nil
}

// startBehaviorMonitor 启动行为监控
func (a *App) startBehaviorMonitor() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 如果已经运行，则不重复启动
	if a.behaviorMonitorCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.behaviorMonitorCancel = cancel

	// 每5分钟检查一次行为详情
	interval := 5 * time.Minute

	go func() {
		ticker := time.NewTicker(interval)
		a.monitorBehaviorAndRescan()
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("行为监控已停止")
				return
			case <-ticker.C:
				log.Printf("开始检查行为详情...")
				if err := a.monitorBehaviorAndRescan(); err != nil {
					log.Printf("行为监控失败: %v", err)
				}
			}
		}
	}()

	log.Printf("行为监控已启动，检查间隔: %v", interval)
}

// ensureDefaultUser ensures that a default user exists in the database
func (a *App) ensureDefaultUser() error {
	count, err := a.userRepo.Count()
	if err != nil {
		return err
	}

	// 如果还没有用户，创建一个默认用户（未设置密码）
	if count == 0 {
		defaultUser := &User{
			Username:     "admin",
			PasswordHash: "", // 空密码哈希表示未设置密码
			IsSetup:      false,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		if err := a.userRepo.Create(defaultUser); err != nil {
			return err
		}
		log.Printf("Created default user: admin (password not set)")
	}
	return nil
}

func (a *App) scanOnce() (created, updated, skipped int, err error) {
	startTime := time.Now()

	a.mu.Lock()
	cfg := a.cfg
	c, cerr := a.ensureClientLocked()
	a.mu.Unlock()
	if cerr != nil {
		a.setLastErr(cerr)
		return 0, 0, 0, cerr
	}
	if cfg.OutputDir == "" {
		err := fmt.Errorf("outputDir is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}
	if cfg.UrlPrefix == "" {
		err := fmt.Errorf("urlPrefix is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	if cfg.SelectedCID == "" {
		err := fmt.Errorf("selectedCid is empty")
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		a.setLastErr(err)
		return 0, 0, 0, err
	}

	// Create scan history record
	scanHistory := &ScanHistory{
		ScanTime: startTime,
		RootCID:  cfg.SelectedCID,
		MaxDepth: cfg.MaxDepth,
		Status:   "running",
	}
	if err := a.scanRepo.Create(scanHistory); err != nil {
		log.Printf("Failed to create scan history: %v", err)
	}

	// Get root folder info from 115 API to get create and update time
	rootInfo, err := a.rateLimitedStat(c, cfg.SelectedCID)
	rootCreateTime := time.Time{}
	rootUpdateTime := time.Time{}
	if err != nil {
		log.Printf("Failed to get root folder info: %v", err)
	} else {
		rootCreateTime = rootInfo.CreateTime
		rootUpdateTime = rootInfo.UpdateTime
	}

	// Create root folder state in database
	rootFolderState := &FileState{
		FileID:        cfg.SelectedCID,
		PickCode:      "",     // 根目录可能没有pickcode
		FileName:      "Root", // 根目录名称
		FilePath:      "",     // 根目录路径为空
		StrmPath:      "",     // 根目录没有.strm文件
		URL:           "",     // 根目录没有URL
		Size:          0,
		IsDir:         true,
		ParentFileID:  "", // 根目录没有父文件夹
		Depth:         0,
		FolderPath:    "",             // 根目录文件夹路径为空
		LastScannedAt: startTime,      // 设置扫描时间
		CreateTime:    rootCreateTime, // 根目录的创建时间
		UpdateTime:    rootUpdateTime, // 根目录的更新时间
	}
	if err := a.fileRepo.Upsert(rootFolderState); err != nil {
		log.Printf("Failed to upsert root folder state: %v", err)
	}

	created, updated, skipped, err = a.scanCIDRecursive(c, cfg, cfg.SelectedCID, "", 0, false) // false表示全量扫描
	if err != nil {
		a.setLastErr(err)
		scanHistory.Status = "failed"
		scanHistory.Message = err.Error()
		a.scanRepo.Update(scanHistory)
		return created, updated, skipped, err
	}

	// Update scan history
	duration := time.Since(startTime)
	scanHistory.Created = created
	scanHistory.Updated = updated
	scanHistory.Skipped = skipped
	scanHistory.Total = created + updated + skipped
	scanHistory.Duration = duration.String()
	scanHistory.Status = "success"
	scanHistory.Message = "Scan completed successfully"
	a.scanRepo.Update(scanHistory)

	a.mu.Lock()
	a.lastScanAt = time.Now()
	a.lastScanMsg = fmt.Sprintf("created=%d updated=%d skipped=%d", created, updated, skipped)
	a.lastErr = ""
	a.mu.Unlock()

	return created, updated, skipped, nil
}

// startAutoScan starts automatic scheduled scanning
func (a *App) startAutoScan() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// If already running, do nothing
	if a.scanCancel != nil {
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
				log.Printf("Auto scan stopped")
				return
			case <-ticker.C:
				log.Printf("Running scheduled scan...")
				_, _, _, _ = a.scanIncremental()
			}
		}
	}()

	log.Printf("Auto scan scheduled with interval: %v", interval)
}

// startProxyServer starts a separate proxy server on the configured port
func (a *App) startProxyServer() {
	if !a.cfg.EmbyProxy.Enabled || a.cfg.EmbyProxy.Target == "" || a.cfg.EmbyProxy.ProxyPort == "" {
		log.Printf("Emby proxy not configured properly")
		return
	}

	gin.SetMode(gin.ReleaseMode)
	proxyRouter := gin.Default()

	// Register redirect handler if enabled, otherwise use proxy
	if a.cfg.EmbyRedirect.Enabled {
		proxyRouter.Any("/*proxyPath", a.handleEmbyRedirect)
		log.Printf("Emby redirect server listening on http://127.0.0.1%s", a.cfg.EmbyProxy.ProxyPort)
		log.Printf("Redirecting video streams to 115直链, proxying other requests to: %s", a.cfg.EmbyProxy.Target)
	} else {
		proxyRouter.Any("/*proxyPath", a.handleEmbyProxy)
		log.Printf("Emby proxy server listening on http://127.0.0.1%s", a.cfg.EmbyProxy.ProxyPort)
		log.Printf("Proxying all requests to: %s", a.cfg.EmbyProxy.Target)
	}

	if err := proxyRouter.Run(a.cfg.EmbyProxy.ProxyPort); err != nil {
		log.Printf("Failed to start Emby proxy server: %v", err)
	}
}

// handleEmbyProxy handles reverse proxy requests to Emby server
func (a *App) handleEmbyProxy(c *gin.Context) {
	if !a.cfg.EmbyProxy.Enabled || a.cfg.EmbyProxy.Target == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Emby proxy is not enabled"})
		return
	}

	target, err := url.Parse(a.cfg.EmbyProxy.Target)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Invalid target URL: %v", err)})
		return
	}

	// Log request details
	//a.logProxyRequest(c, target)

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Modify the request
	proxy.Director = func(req *http.Request) {
		// Set the target host
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host

		// Handle path
		proxyPath := c.Param("proxyPath")
		if a.cfg.EmbyProxy.Path == "/" {
			// If path is root, use the proxy path directly
			req.URL.Path = proxyPath
		} else if a.cfg.EmbyProxy.StripPath {
			// Remove the proxy path prefix
			req.URL.Path = proxyPath
		} else {
			// Keep the full path including the proxy prefix
			req.URL.Path = a.cfg.EmbyProxy.Path + proxyPath
		}

		// Preserve query parameters
		req.URL.RawQuery = c.Request.URL.RawQuery

		// Copy headers
		req.Header = c.Request.Header.Clone()
		req.Host = target.Host

		// Remove X-Forwarded-Host to avoid conflicts
		req.Header.Del("X-Forwarded-Host")
		req.Header.Set("X-Forwarded-For", c.ClientIP())
		req.Header.Set("X-Forwarded-Proto", c.Request.URL.Scheme)

		// Log modified request details
		//a.logModifiedRequest(req, c)
	}

	// Create a custom response writer to capture response
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Log response details
		//a.logProxyResponse(resp, c)
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Emby proxy error: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Proxy error: %v", err)})
	}

	// Serve the request
	proxy.ServeHTTP(c.Writer, c.Request)
}

func (a *App) setLastErr(err error) {
	a.mu.Lock()
	a.lastErr = err.Error()
	a.mu.Unlock()
}

// logProxyRequest logs the details of the incoming proxy request
func (a *App) logProxyRequest(c *gin.Context, target *url.URL) {
	log.Printf("[PROXY REQUEST]")
	log.Printf("  Client: %s", c.ClientIP())
	log.Printf("  Method: %s", c.Request.Method)
	log.Printf("  Original URL: %s", c.Request.URL.String())
	log.Printf("  Target: %s", target.String())
	log.Printf("  Path: %s", c.Param("proxyPath"))
	log.Printf("  Query: %s", c.Request.URL.RawQuery)

	// Log headers (excluding sensitive ones)
	log.Printf("  Headers:")
	for name, values := range c.Request.Header {
		// Skip sensitive headers
		if strings.EqualFold(name, "Authorization") ||
			strings.EqualFold(name, "Cookie") ||
			strings.EqualFold(name, "X-API-Key") {
			log.Printf("    %s: [REDACTED]", name)
			continue
		}
		for _, value := range values {
			log.Printf("    %s: %s", name, value)
		}
	}

	// Log request body if present and small
	if c.Request.ContentLength > 0 && c.Request.ContentLength < 1024 {
		body, err := c.GetRawData()
		if err == nil && len(body) > 0 {
			// Restore the body for further processing
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
			log.Printf("  Body: %s", string(body))
		}
	}
}

// logModifiedRequest logs the modified request that will be sent to target
func (a *App) logModifiedRequest(req *http.Request, c *gin.Context) {
	log.Printf("[MODIFIED REQUEST TO TARGET]")
	log.Printf("  Method: %s", req.Method)
	log.Printf("  URL: %s", req.URL.String())
	log.Printf("  Host: %s", req.Host)

	log.Printf("  Headers:")
	for name, values := range req.Header {
		// Skip sensitive headers
		if strings.EqualFold(name, "Authorization") ||
			strings.EqualFold(name, "Cookie") ||
			strings.EqualFold(name, "X-API-Key") {
			log.Printf("    %s: [REDACTED]", name)
			continue
		}
		for _, value := range values {
			log.Printf("    %s: %s", name, value)
		}
	}
}

// logProxyResponse logs the response from the target server
func (a *App) logProxyResponse(resp *http.Response, c *gin.Context) {
	log.Printf("[PROXY RESPONSE]")
	log.Printf("  Status: %d %s", resp.StatusCode, resp.Status)
	log.Printf("  Content-Length: %d", resp.ContentLength)

	log.Printf("  Headers:")
	for name, values := range resp.Header {
		for _, value := range values {
			log.Printf("    %s: %s", name, value)
		}
	}

	// Log response body for certain content types
	contentType := resp.Header.Get("Content-Type")
	isJSON := strings.Contains(contentType, "application/json")
	isText := strings.Contains(contentType, "text/")

	if (isJSON || isText) && resp.ContentLength > 0 && resp.ContentLength < 4096 {
		// Read and log the response body
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			// Restore the body for further processing
			resp.Body = io.NopCloser(bytes.NewBuffer(body))

			if isJSON {
				// Try to pretty print JSON
				var prettyJSON bytes.Buffer
				if json.Indent(&prettyJSON, body, "", "  ") == nil {
					log.Printf("  Body (JSON):\n%s", prettyJSON.String())
				} else {
					log.Printf("  Body: %s", string(body))
				}
			} else {
				log.Printf("  Body: %s", string(body))
			}
		}
	} else if resp.ContentLength > 0 {
		log.Printf("  Body: [Binary data, size: %d bytes, type: %s]", resp.ContentLength, contentType)
	}
}

// hasExtension checks if a filename has one of the given extensions
func hasExtension(filename string, extensions []string) bool {
	if len(extensions) == 0 {
		return true // no filter means all files
	}
	ext := strings.ToLower(filepath.Ext(filename))
	for _, e := range extensions {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

// shouldDownload checks if a file should be downloaded directly based on its extension
func shouldDownload(filename string, downloadExtensions []string) bool {
	return hasExtension(filename, downloadExtensions)
}

// downloadFile downloads a file from 115 to local path
func (a *App) downloadFile(c *driver.Pan115Client, pickCode, filename, localPath string) error {
	info, err := a.rateLimitedDownload(c, pickCode)
	if err != nil {
		return err
	}

	// if info.Url.Url == "" {
	// 	return fmt.Errorf("no download url available for %s", filename)
	// }

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}

	rs, err := info.Get()
	if err != nil {
		return err
	}
	f, _ := os.Create(localPath)
	defer func() {
		f.Close()
	}()
	_, err = f.ReadFrom(rs)
	return err
}

func (a *App) scanCIDRecursive(c *driver.Pan115Client, cfg Config, cid string, relPath string, depth int, incremental bool) (created, updated, skipped int, err error) {
	// 检查文件夹是否已经扫描过（仅用于日志记录）
	if incremental {
		existingFolder, err := a.fileRepo.GetByFileID(cid)
		if err == nil && existingFolder.IsDir && !existingFolder.LastScannedAt.IsZero() {
			log.Printf("Folder already scanned: %s (CID: %s), last scanned at: %v",
				existingFolder.FileName, cid, existingFolder.LastScannedAt)
		}
	}

	// 即使文件夹已经扫描过，我们仍然需要获取文件列表来检查子目录和文件的变动
	files, err := a.rateLimitedList(c, cid)
	if err != nil {
		return 0, 0, 0, err
	}

	// 更新文件夹的最后扫描时间
	if incremental {
		// 获取或创建文件夹记录
		folderName := "Root"
		if relPath != "" {
			// 从relPath中提取文件夹名
			parts := strings.Split(relPath, "/")
			if len(parts) > 0 {
				folderName = parts[len(parts)-1]
			}
		}

		folderState := &FileState{
			FileID:        cid,
			PickCode:      "", // 文件夹可能没有pickcode
			FileName:      folderName,
			FilePath:      relPath,
			StrmPath:      "",
			URL:           "",
			Size:          0,
			IsDir:         true,
			ParentFileID:  "", // 会在下面的循环中设置
			Depth:         depth,
			FolderPath:    relPath,
			LastScannedAt: time.Now(),
			CreateTime:    time.Time{}, // 文件夹没有创建时间
			UpdateTime:    time.Time{}, // 文件夹没有更新时间
		}

		if err := a.fileRepo.Upsert(folderState); err != nil {
			log.Printf("Failed to update folder scan time: %v", err)
		}
	}

	// Count names per-folder (avoid collisions only within same folder)
	nameCount := map[string]int{}
	for _, f := range *files {
		if f.IsDirectory {
			continue
		}
		if f.PickCode == "" {
			continue
		}
		// Check if file should be scanned based on extensions
		if !hasExtension(f.Name, cfg.ScanExtensions) && !hasExtension(f.Name, cfg.DownloadExtensions) {
			continue
		}
		base := sanitizeFileName(f.Name)
		nameCount[base]++
	}

	// Ensure output subdir exists
	outDir := filepath.Join(cfg.OutputDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, 0, 0, err
	}

	// Ensure download subdir exists
	downloadOutDir := filepath.Join(cfg.OutputDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(downloadOutDir, 0o755); err != nil {
		return 0, 0, 0, err
	}

	for _, f := range *files {
		if f.IsDirectory {
			if depth >= cfg.MaxDepth {
				continue
			}

			// Create folder path
			childRel := relPath
			if childRel != "" {
				childRel += "/"
			}
			childRel += sanitizeFileName(f.Name)

			// Create/update folder state in database
			folderState := &FileState{
				FileID:        f.FileID,
				PickCode:      f.PickCode,
				FileName:      f.Name,
				FilePath:      childRel, // 文件夹自身的路径
				StrmPath:      "",       // 文件夹没有.strm文件
				URL:           "",       // 文件夹没有URL
				Size:          f.Size,
				IsDir:         true,
				ParentFileID:  cid, // 当前目录的CID作为父文件夹ID
				Depth:         depth,
				FolderPath:    childRel,     // 文件夹自身的路径
				LastScannedAt: time.Now(),   // 设置扫描时间
				CreateTime:    f.CreateTime, // 网盘文件夹的创建时间
				UpdateTime:    f.UpdateTime, // 网盘文件夹的更新时间
			}

			if err := a.fileRepo.Upsert(folderState); err != nil {
				log.Printf("Failed to upsert folder state: %v", err)
			}

			cCreated, cUpdated, cSkipped, cErr := a.scanCIDRecursive(c, cfg, f.FileID, childRel, depth+1, incremental)
			created += cCreated
			updated += cUpdated
			skipped += cSkipped
			if cErr != nil {
				return created, updated, skipped, cErr
			}
			continue
		}

		// file
		if f.PickCode == "" {
			skipped++
			continue
		}

		// Check if file should be scanned based on extensions
		if !hasExtension(f.Name, cfg.ScanExtensions) && !hasExtension(f.Name, cfg.DownloadExtensions) {
			skipped++
			continue
		}

		base := sanitizeFileName(f.Name)
		baseWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))

		if nameCount[baseWithoutExt] > 1 {
			baseWithoutExt = fmt.Sprintf("%s__%s", baseWithoutExt, f.FileID)
		}

		// Check if file should be downloaded directly
		if shouldDownload(f.Name, cfg.DownloadExtensions) {
			// Download file directly
			downloadPath := filepath.Join(downloadOutDir, base)

			downloadPath = filepath.ToSlash(downloadPath)
			// Check if file already exists and has same size
			if fi, err := os.Stat(downloadPath); err == nil && fi.Size() == f.Size {
				skipped++
				continue
			}

			// Download the file
			if err := a.downloadFile(c, f.PickCode, f.Name, downloadPath); err != nil {
				log.Printf("Failed to download file %s: %v", f.Name, err)
				skipped++
				continue
			}

			// Create/update file state in database for downloaded file
			fileState := &FileState{
				FileID:        f.FileID,
				PickCode:      f.PickCode,
				FileName:      f.Name,
				FilePath:      relPath,
				StrmPath:      downloadPath,
				URL:           "", // No URL for downloaded files
				Size:          f.Size,
				IsDir:         f.IsDirectory,
				ParentFileID:  cid, // 当前目录的CID作为父文件夹ID
				Depth:         depth,
				FolderPath:    relPath,      // 文件所在文件夹路径
				LastScannedAt: time.Now(),   // 设置扫描时间
				CreateTime:    f.CreateTime, // 网盘文件的创建时间
				UpdateTime:    f.UpdateTime, // 网盘文件的更新时间
			}

			if err := a.fileRepo.Upsert(fileState); err != nil {
				log.Printf("Failed to upsert file state: %v", err)
			}

			created++
			continue
		}

		// Generate .strm file for non-download files
		outPath := filepath.Join(outDir, baseWithoutExt+".strm")
		outPath = filepath.ToSlash(outPath)
		url := cfg.UrlPrefix + "/d/" + f.PickCode + "?" + f.Name

		// Check database for existing state
		existingState, err := a.fileRepo.GetByFileID(f.FileID)
		shouldUpdate := false

		if err == nil && existingState.PickCode == f.PickCode {
			// File exists and pickcode hasn't changed, check if URL matches
			if b, rerr := os.ReadFile(outPath); rerr == nil {
				if strings.TrimSpace(string(b)) == url {
					skipped++
					continue
				}
			}
			shouldUpdate = true
		}

		// Create/update file state in database
		fileState := &FileState{
			FileID:        f.FileID,
			PickCode:      f.PickCode,
			FileName:      f.Name,
			FilePath:      relPath,
			StrmPath:      outPath,
			URL:           url,
			Size:          f.Size,
			IsDir:         f.IsDirectory,
			ParentFileID:  cid, // 当前目录的CID作为父文件夹ID
			Depth:         depth,
			FolderPath:    relPath,      // 文件所在文件夹路径
			LastScannedAt: time.Now(),   // 设置扫描时间
			CreateTime:    f.CreateTime, // 网盘文件的创建时间
			UpdateTime:    f.UpdateTime, // 网盘文件的更新时间
		}

		if err := a.fileRepo.Upsert(fileState); err != nil {
			log.Printf("Failed to upsert file state: %v", err)
		}

		_, statErr := os.Stat(outPath)
		if shouldUpdate || statErr != nil {
			if shouldUpdate || statErr != nil {
				updated++
			} else {
				created++
			}
		} else {
			skipped++
			continue
		}

		if werr := os.WriteFile(outPath, []byte(url+"\n"), 0o644); werr != nil {
			return created, updated, skipped, werr
		}
	}

	return created, updated, skipped, nil
}
