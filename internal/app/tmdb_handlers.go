package app

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// handleTMDBConfig 处理 TMDB 配置
func (a *App) handleTMDBConfig(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		a.mu.Lock()
		tmdbConfig := a.cfg.TMDB
		a.mu.Unlock()
		c.JSON(http.StatusOK, tmdbConfig)
		return
	case http.MethodPost:
		var patch struct {
			APIKey *string `json:"apiKey"`
		}
		if err := c.ShouldBindJSON(&patch); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		a.mu.Lock()
		if patch.APIKey != nil {
			a.cfg.TMDB.APIKey = *patch.APIKey
			// 重新初始化 TMDB 客户端
			if a.cfg.TMDB.APIKey != "" {
				a.tmdbClient = NewTMDBClient(a.cfg.TMDB.APIKey)
			} else {
				a.tmdbClient = nil
			}
		}
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

// handleSearchMovies 处理电影搜索
func (a *App) handleSearchMovies(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	var req struct {
		Query        string `json:"query"`
		IncludeAdult bool   `json:"include_adult"`
		Language     string `json:"language"`
		Page         int    `json:"page"`
		Year         int    `json:"year"`
		Region       string `json:"region"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 设置默认值
	if req.Language == "" {
		req.Language = "zh-CN"
	}
	if req.Page <= 0 {
		req.Page = 1
	}

	searchReq := SearchMovieRequest{
		Query:        req.Query,
		IncludeAdult: req.IncludeAdult,
		Language:     req.Language,
		Page:         req.Page,
		Year:         req.Year,
		Region:       req.Region,
	}

	result, err := a.tmdbClient.SearchMovies(searchReq)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleSearchTV 处理电视剧搜索
func (a *App) handleSearchTV(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	var req struct {
		Query        string `json:"query"`
		IncludeAdult bool   `json:"include_adult"`
		Language     string `json:"language"`
		Page         int    `json:"page"`
		Year         int    `json:"year"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 设置默认值
	if req.Language == "" {
		req.Language = "zh-CN"
	}
	if req.Page <= 0 {
		req.Page = 1
	}

	searchReq := SearchTVRequest{
		Query:        req.Query,
		IncludeAdult: req.IncludeAdult,
		Language:     req.Language,
		Page:         req.Page,
		Year:         req.Year,
	}

	result, err := a.tmdbClient.SearchTV(searchReq)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleGetMovieDetails 处理获取电影详情
func (a *App) handleGetMovieDetails(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	movieIDStr := c.Param("movieId")
	movieID, err := strconv.Atoi(movieIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid movie ID"})
		return
	}

	language := c.DefaultQuery("language", "zh-CN")

	result, err := a.tmdbClient.GetMovieDetails(movieID, language)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleGetTVDetails 处理获取电视剧详情
func (a *App) handleGetTVDetails(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	tvIDStr := c.Param("tvId")
	tvID, err := strconv.Atoi(tvIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid TV ID"})
		return
	}

	language := c.DefaultQuery("language", "zh-CN")

	result, err := a.tmdbClient.GetTVDetails(tvID, language)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleSearchMoviesWithQuery 使用查询参数搜索电影（简化版）
func (a *App) handleSearchMoviesWithQuery(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	query := c.Query("query")
	if query == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "query parameter is required"})
		return
	}

	language := c.DefaultQuery("language", "zh-CN")
	pageStr := c.DefaultQuery("page", "1")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page <= 0 {
		page = 1
	}

	result, err := a.tmdbClient.SearchMoviesWithQuery(query, 0, language, page)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleSearchTVWithQuery 使用查询参数搜索电视剧（简化版）
func (a *App) handleSearchTVWithQuery(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	query := c.Query("query")
	if query == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "query parameter is required"})
		return
	}

	language := c.DefaultQuery("language", "zh-CN")
	pageStr := c.DefaultQuery("page", "1")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page <= 0 {
		page = 1
	}

	result, err := a.tmdbClient.SearchTVWithQuery(query, 0, language, page)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleFormatImageURL 处理图片URL格式化
func (a *App) handleFormatImageURL(c *gin.Context) {
	// 检查 TMDB 客户端是否已初始化
	if a.tmdbClient == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB API key not configured"})
		return
	}

	path := c.Query("path")
	size := c.DefaultQuery("size", "w500")
	imageType := c.DefaultQuery("type", "poster")

	if path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "path parameter is required"})
		return
	}

	var url string
	if imageType == "backdrop" {
		url = a.tmdbClient.FormatBackdropURL(path, size)
	} else {
		url = a.tmdbClient.FormatPosterURL(path, size)
	}

	c.JSON(http.StatusOK, gin.H{
		"url":  url,
		"path": path,
		"size": size,
		"type": imageType,
	})
}
