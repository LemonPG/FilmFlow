package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MediaSource represents Emby/Jellyfin media source
type MediaSource struct {
	ID                     string `json:"Id"`
	Path                   string `json:"Path"`
	Protocol               string `json:"Protocol"`
	IsRemote               bool   `json:"IsRemote"`
	IsInfiniteStream       bool   `json:"IsInfiniteStream"`
	SupportsDirectPlay     bool   `json:"SupportsDirectPlay"`
	SupportsDirectStream   bool   `json:"SupportsDirectStream"`
	SupportsTranscoding    bool   `json:"SupportsTranscoding"`
	TranscodingUrl         string `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol string `json:"TranscodingSubProtocol,omitempty"`
	TranscodingContainer   string `json:"TranscodingContainer,omitempty"`
	Container              string `json:"Container"`
	Bitrate                int    `json:"Bitrate"`
	Name                   string `json:"Name"`
	DirectStreamUrl        string `json:"DirectStreamUrl,omitempty"`
	ETag                   string `json:"ETag,omitempty"`
}

// PlaybackInfoResponse represents Emby/Jellyfin playback info response
type PlaybackInfoResponse struct {
	MediaSources     []MediaSource `json:"MediaSources"`
	PlaySessionId    string        `json:"PlaySessionId"`
	IsInfiniteStream bool          `json:"IsInfiniteStream,omitempty"`
}

// ItemInfo represents Emby/Jellyfin item info
type ItemInfo struct {
	Items []struct {
		ID           string        `json:"Id"`
		Name         string        `json:"Name"`
		Path         string        `json:"Path"`
		MediaSources []MediaSource `json:"MediaSources,omitempty"`
	} `json:"Items"`
}

// responseBodyCapture is a response writer that captures the response body
type responseBodyCapture struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (r *responseBodyCapture) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// handleEmbyRedirect handles Emby requests with full proxy and redirect capabilities
func (a *App) handleEmbyRedirect(c *gin.Context) {
	if !a.cfg.EmbyRedirect.Enabled {
		// If redirect is not enabled, just proxy the request
		a.handleEmbyProxy(c)
		return
	}

	path := c.Request.URL.Path
	_ = c.Request.Method // Suppress unused warning

	lowerPath := strings.ToLower(path)
	// Route different API endpoints based on emby.conf
	switch {
	// Video stream requests - core redirect functionality
	case strings.Contains(lowerPath, "/videos/") && (strings.Contains(lowerPath, "/stream") || strings.Contains(lowerPath, "/original")):
		a.redirect2Pan(c)
		return

	// Live stream requests
	case strings.Contains(lowerPath, "/videos/") && strings.Contains(lowerPath, "/live"):
		a.redirectLiveStream(c)
		return

	// Master stream requests (transcoded)
	case strings.Contains(lowerPath, "/videos/") && strings.Contains(lowerPath, "/master"):
		a.redirectMasterStream(c)
		return

	// Audio stream requests
	case strings.Contains(lowerPath, "/audio/") && (strings.Contains(lowerPath, "/stream") || strings.Contains(lowerPath, "/universal")):
		a.redirectAudioStream(c)
		return

	// PlaybackInfo requests
	case strings.Contains(lowerPath, "/playbackinfo"):
		a.transferPlaybackInfo(c)
		return

	// Download requests
	case strings.Contains(lowerPath, "/items/") && strings.Contains(lowerPath, "/download"):
		a.redirect2Pan(c)
		return

	// Sync/JobItems download requests
	case strings.Contains(lowerPath, "/sync/jobItems/") && strings.Contains(lowerPath, "/file"):
		a.redirectSyncDownload(c)
		return

	// Users Items requests
	case strings.Contains(lowerPath, "/users/") && strings.Contains(lowerPath, "/items"):
		if strings.Contains(path, "/latest") {
			a.handleUsersItemsLatest(c)
		} else {
			a.handleUsersItems(c)
		}
		return

	// Items Similar requests
	case strings.Contains(lowerPath, "/items/") && strings.Contains(lowerPath, "/similar"):
		a.handleItemsSimilar(c)
		return

	// System Info requests
	case strings.Contains(lowerPath, "/system/info"):
		a.handleSystemInfo(c)
		return

	// Sessions Playing requests
	case strings.Contains(lowerPath, "/sessions/playing"):
		a.handleSessionsPlaying(c)
		return

	// Active Encodings requests
	case strings.Contains(lowerPath, "/videos/activeEncodings"):
		a.handleActiveEncodings(c)
		return

	// Subtitle requests (virtual media)
	case strings.Contains(lowerPath, "/videos/") && strings.Contains(lowerPath, "/subtitles") && strings.Contains(lowerPath, "virtual"):
		a.handleVirtualSubtitles(c)
		return

	// Base HTML player modification
	case strings.Contains(lowerPath, "/web/playback") || strings.Contains(lowerPath, "/web/index.html"):
		a.modifyBaseHtmlPlayer(c)
		return

	// WebSocket/Socket requests
	case strings.Contains(lowerPath, "/socket") || strings.Contains(lowerPath, "/embywebsocket"):
		a.handleWebSocket(c)
		return
	}

	// For all other requests, just proxy
	a.handleEmbyProxy(c)
}

// redirect2Pan implements the main redirect logic using built-in handleDownload
func (a *App) redirect2Pan(c *gin.Context) {
	// Extract item ID from path using emby2Alist logic
	itemId := extractItemIdFromUri(c.Request.URL.Path)

	// Fallback to path-based extraction if regex fails
	if itemId == "" {
		pathParts := strings.Split(c.Request.URL.Path, "/")
		for i, part := range pathParts {
			if strings.ToLower(part) == "videos" && i+1 < len(pathParts) {
				itemId = pathParts[i+1]
				break
			}
		}
	}

	if itemId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "itemId not found in path"})
		return
	}

	// Fetch Emby file info to get the pickcode from the path
	// The path in strm files contains the pickcode
	embyRes, err := a.fetchEmbyFilePath(c, itemId)
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.internalRedirect(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Check if redirect is allowed using route manager
	action := a.routeManager.EvaluateRequest(c, embyRes.Path, embyRes.MediaSource)
	if action != "redirect" && action != "transcode" {
		if action == "block" || action == "blockPlay" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Playback blocked by routing rules"})
			return
		}
		a.internalRedirect(c)
		return
	}

	// Extract pickcode from the path
	// The path format in strm files is like: http://127.0.0.1:17615/d/{pickcode}?{filename}

	fmt.Printf("embyRes path:%s\n", embyRes.Path)

	pickCode := extractPickCodeFromPath(embyRes.Path)
	if pickCode == "" {
		// If no pickcode found, fallback to original link
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.internalRedirect(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no pickcode found in path"})
		return
	}

	// Use the built-in handleDownload logic to get 115直链
	a.mu.Lock()
	client, err := a.ensureClientLocked()
	a.mu.Unlock()
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.internalRedirect(c)
			return
		}
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
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.internalRedirect(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if info.Url.Url == "" {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.internalRedirect(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no download url available"})
		return
	}

	// Return 302 redirect to 115直链
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate")
	fmt.Printf("return redirect url:%s\n", info.Url.Url)
	c.Redirect(http.StatusFound, info.Url.Url)
}

// allowRedirect checks if redirect is allowed for this request
func (a *App) allowRedirect(c *gin.Context) bool {
	// Use route manager to evaluate the request
	action := a.routeManager.EvaluateRequest(c, "", nil)

	// Allow redirect if action is "redirect" or "transcode"
	return action == "redirect" || action == "transcode"
}

// fetchEmbyFilePath fetches file path from Emby API based on emby2Alist logic
func (a *App) fetchEmbyFilePath(c *gin.Context, itemId string) (*EmbyFileResult, error) {
	// Build Emby API URL based on emby2Alist logic
	embyHost := a.cfg.EmbyProxy.Target
	if embyHost == "" {
		embyHost = "http://localhost:8096"
	}

	// Get parameters from request
	mediaSourceId := c.Query("MediaSourceId")
	if mediaSourceId == "" {
		mediaSourceId = c.Query("mediaSourceId")
	}
	etag := c.Query("Tag")
	apiKey := c.Query("api_key")
	if apiKey == "" {
		// Try to get from header
		apiKey = c.GetHeader("X-MediaBrowser-Token")
		if apiKey == "" {
			apiKey = a.cfg.EmbyApiKey
		}
	}

	var apiUrl string
	if strings.Contains(c.Request.URL.Path, "JobItems") {
		apiUrl = fmt.Sprintf("%s/Sync/JobItems?api_key=%s", embyHost, apiKey)
	} else if mediaSourceId != "" {
		// Handle mediaSourceId format like "mediasource_447039"
		cleanMediaSourceId := mediaSourceId
		if strings.HasPrefix(mediaSourceId, "mediasource_") {
			cleanMediaSourceId = strings.Replace(mediaSourceId, "mediasource_", "", 1)
		}
		apiUrl = fmt.Sprintf("%s/Items?Ids=%s&Fields=Path,MediaSources&Limit=1&api_key=%s", embyHost, cleanMediaSourceId, apiKey)
	} else {
		apiUrl = fmt.Sprintf("%s/Items?Ids=%s&Fields=Path,MediaSources&Limit=1&api_key=%s", embyHost, itemId, apiKey)
	}

	// Make request to Emby API
	req, err := http.NewRequest("GET", apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("Emby API request failed: %v", err)
	}

	// Copy headers from original request
	// for k, v := range c.Request.Header {
	// 	req.Header[k] = v
	// }
	// Set proper headers for Emby API
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	req.Header.Set("Content-Length", "0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Emby API request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Emby API returned status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read Emby API response: %v", err)
	}

	// Handle JobItems response
	if strings.Contains(c.Request.URL.Path, "JobItems") {
		var jobItemsResponse struct {
			Items []struct {
				ID         string `json:"Id"`
				OutputPath string `json:"OutputPath"`
			} `json:"Items"`
		}
		if err := json.Unmarshal(body, &jobItemsResponse); err != nil {
			return nil, fmt.Errorf("Failed to parse JobItems response: %v", err)
		}

		if len(jobItemsResponse.Items) == 0 {
			return nil, fmt.Errorf("no job items found for ID: %s", itemId)
		}

		for _, item := range jobItemsResponse.Items {
			if item.ID == itemId {
				result := &EmbyFileResult{
					Path:     item.OutputPath,
					ItemName: itemId,
					NotLocal: isStrmFile(item.OutputPath),
				}
				return result, nil
			}
		}
		return nil, fmt.Errorf("job item not found: %s", itemId)
	}

	// Handle regular Items response
	var itemInfo ItemInfo
	if err := json.Unmarshal(body, &itemInfo); err != nil {
		return nil, fmt.Errorf("Failed to parse Items response: %v", err)
	}

	if len(itemInfo.Items) == 0 {
		return nil, fmt.Errorf("no items found for ID: %s", itemId)
	}

	item := itemInfo.Items[0]
	result := &EmbyFileResult{
		Path:     "",
		ItemName: item.Name,
		NotLocal: false,
	}

	// Handle MediaSources (could be multiple in Jellyfin)
	if len(item.MediaSources) > 0 {
		var selectedMediaSource MediaSource

		// If ETag is provided, try to find matching MediaSource
		if etag != "" {
			for _, ms := range item.MediaSources {
				if ms.ETag == etag {
					selectedMediaSource = ms
					break
				}
			}
		}

		// If no match found or no ETag, try MediaSourceId
		if selectedMediaSource.ID == "" && mediaSourceId != "" {
			for _, ms := range item.MediaSources {
				if ms.ID == mediaSourceId {
					selectedMediaSource = ms
					break
				}
			}
		}

		// If still no match, use first MediaSource
		if selectedMediaSource.ID == "" {
			selectedMediaSource = item.MediaSources[0]
		}

		result.Path = selectedMediaSource.Path
		result.MediaSource = &selectedMediaSource

		// Check if it's remote, infinite stream, or strm file
		result.NotLocal = selectedMediaSource.IsInfiniteStream ||
			selectedMediaSource.IsRemote ||
			isStrmFile(item.Path) // Check item.Path as in original
	} else {
		// No MediaSources (could be Photo or other media type)
		result.Path = item.Path
	}

	return result, nil
}

// transferPlaybackInfo handles playback info requests
func (a *App) transferPlaybackInfo(c *gin.Context) {
	if !a.cfg.EmbyRedirect.PlaybackInfoConfig {
		// If playback info config is not enabled, just proxy the request
		a.handleEmbyProxy(c)
		return
	}

	// Create a custom response writer to intercept and modify the response
	responseWriter := &responseBodyCapture{
		ResponseWriter: c.Writer,
		body:           &bytes.Buffer{},
	}

	// Temporarily replace the response writer
	originalWriter := c.Writer
	c.Writer = responseWriter

	// Proxy the request to get original playback info
	a.proxyRequest(c, responseWriter)

	// Restore the original writer
	c.Writer = originalWriter

	// Get the response body
	responseBody := responseWriter.body.Bytes()

	// Parse the JSON response
	var playbackInfo PlaybackInfoResponse
	if err := json.Unmarshal(responseBody, &playbackInfo); err != nil {
		// If parsing fails, just return the original response
		c.Data(responseWriter.Status(), responseWriter.Header().Get("Content-Type"), responseBody)
		return
	}

	// Modify the media sources to add direct stream URLs
	for i := range playbackInfo.MediaSources {
		source := &playbackInfo.MediaSources[i]

		// Check if this is a strm file with pickcode
		if isStrmFile(source.Path) {
			pickCode := extractPickCodeFromPath(source.Path)
			if pickCode != "" {
				// Generate direct stream URL using our handleDownload endpoint
				directStreamURL := fmt.Sprintf("%s/d/%s", a.cfg.UrlPrefix, pickCode)
				if source.Name != "" {
					directStreamURL += fmt.Sprintf("?%s", source.Name)
				}

				// Update the source with direct stream URL
				source.DirectStreamUrl = directStreamURL
				source.SupportsDirectStream = true
				source.SupportsDirectPlay = true
				source.Protocol = "Http"

				// Add a name to indicate it's a direct link
				if source.Name == "" {
					source.Name = "115直链"
				} else {
					source.Name += " (115直链)"
				}
			}
		}
	}

	// Marshal the modified response back to JSON
	modifiedResponse, err := json.Marshal(playbackInfo)
	if err != nil {
		// If marshaling fails, just return the original response
		c.Data(responseWriter.Status(), responseWriter.Header().Get("Content-Type"), responseBody)
		return
	}

	// Return the modified response
	c.Data(responseWriter.Status(), "application/json", modifiedResponse)
}

// modifyBaseHtmlPlayer modifies HTML player responses
func (a *App) modifyBaseHtmlPlayer(c *gin.Context) {
	// Proxy the request
	a.handleEmbyProxy(c)

	// Note: In the JavaScript version, this function modifies the HTML response
	// In Go, we would need to intercept and modify the response body
	// For now, we'll just proxy the request
}

// internalRedirect performs an internal redirect (proxy)
func (a *App) internalRedirect(c *gin.Context) {
	// Just proxy the request
	a.handleEmbyProxy(c)
}

// proxyRequest proxies the request to the target Emby server
func (a *App) proxyRequest(c *gin.Context, w http.ResponseWriter) {
	if !a.cfg.EmbyProxy.Enabled || a.cfg.EmbyProxy.Target == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Emby proxy is not enabled"})
		return
	}

	// Build the target URL
	targetURL := a.cfg.EmbyProxy.Target + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	// Create a new request
	req, err := http.NewRequest(c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Copy headers
	for k, v := range c.Request.Header {
		req.Header[k] = v
	}

	// Make the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, v := range resp.Header {
		w.Header()[k] = v
	}

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		// Log error but don't send response as headers may already be sent
		fmt.Printf("Error copying response body: %v\n", err)
	}
}

// Helper functions

// extractItemIdFromUri extracts item ID from URI using emby2Alist logic
func extractItemIdFromUri(uri string) string {
	// Remove "emby", "Sync", and "-" from URI, then find alphanumeric sequences
	// Based on emby2Alist: /[A-Za-z0-9]+/g
	// uri.replace("emby", "").replace("Sync", "").replace(/-/g, "").match(regex)[1]
	cleaned := strings.ReplaceAll(strings.ReplaceAll(uri, "emby", ""), "Sync", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")

	// Find alphanumeric sequences
	var sequences []string
	current := ""
	for _, r := range cleaned {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			current += string(r)
		} else {
			if current != "" {
				sequences = append(sequences, current)
				current = ""
			}
		}
	}
	if current != "" {
		sequences = append(sequences, current)
	}

	// Return the second sequence (index 1) if available
	if len(sequences) > 1 {
		return sequences[1]
	}
	return ""
}

type EmbyFileResult struct {
	Path        string
	ItemName    string
	NotLocal    bool
	MediaSource *MediaSource
}

func isStrmFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".strm")
}

// extractPickCodeFromPath extracts pickcode from strm file path
// Path format: http://127.0.0.1:17615/d/{pickcode}?{filename}
// redirectLiveStream handles live stream requests
func (a *App) redirectLiveStream(c *gin.Context) {
	// For now, proxy live streams as they need special handling
	// TODO: Implement live stream redirect logic
	a.handleEmbyProxy(c)
}

// redirectMasterStream handles master stream (transcoded) requests
func (a *App) redirectMasterStream(c *gin.Context) {
	// For now, proxy master streams as they are transcoded
	// TODO: Implement master stream redirect logic
	a.handleEmbyProxy(c)
}

// redirectAudioStream handles audio stream requests based on emby.conf location ~* /Audio/(.*)/(universal|stream)
func (a *App) redirectAudioStream(c *gin.Context) {
	// Extract itemId using regex pattern: /Audio/(.*)/(universal|stream)
	// This matches paths like: /Audio/12345/stream or /Audio/abc-def/universal
	path := c.Request.URL.Path
	lowerPath := strings.ToLower(path)

	// Check if this matches the audio stream pattern
	if !strings.Contains(lowerPath, "/audio/") || (!strings.Contains(lowerPath, "/stream") && !strings.Contains(lowerPath, "/universal")) {
		a.handleEmbyProxy(c)
		return
	}

	// Extract itemId using regex-like logic (matching nginx location ~* /Audio/(.*)/(universal|stream))
	var itemId string
	pathParts := strings.Split(path, "/")
	for i := 0; i < len(pathParts)-2; i++ {
		if pathParts[i] == "Audio" && (pathParts[i+2] == "stream" || pathParts[i+2] == "universal") {
			itemId = pathParts[i+1]
			break
		}
	}

	// Handle HEAD requests - proxy directly as per emby.conf
	if c.Request.Method == "HEAD" {
		a.handleEmbyProxy(c)
		return
	}

	// Set API type for routing decisions (similar to emby.conf set $apiType "AudioStreamPlay")
	c.Set("apiType", "AudioStreamPlay")

	// Check if redirect is allowed using route manager
	action := a.routeManager.EvaluateRequest(c, "", nil)
	if action != "redirect" && action != "transcode" {
		if action == "block" || action == "blockPlay" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Audio stream blocked by routing rules"})
			return
		}
		a.handleEmbyProxy(c)
		return
	}

	if itemId == "" {
		// If we can't extract itemId, try to get it from query parameters
		itemId = c.Query("itemId")
		if itemId == "" {
			a.handleEmbyProxy(c)
			return
		}
	}

	// Try to get item info and redirect to 115直链 if it's a strm file
	embyRes, err := a.fetchEmbyFilePath(c, itemId)
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Check if this is a remote/stream file that needs redirect
	if !embyRes.NotLocal {
		// Local file, proxy directly
		a.handleEmbyProxy(c)
		return
	}

	pickCode := extractPickCodeFromPath(embyRes.Path)
	if pickCode == "" {
		// If no pickcode found, fallback to original link
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no pickcode found in path"})
		return
	}

	// Use 115直链 for audio
	a.mu.Lock()
	client, err := a.ensureClientLocked()
	a.mu.Unlock()
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
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
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if info.Url.Url == "" {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no download url available"})
		return
	}

	// Add cache control headers as per emby.conf: add_header Cache-Control max-age=3600
	c.Header("Cache-Control", "max-age=3600")
	c.Header("Referrer-Policy", "no-referrer")

	// Return 302 redirect to 115直链
	c.Redirect(http.StatusFound, info.Url.Url)
}

// redirectDownload handles download requests based on emby.conf location ~* /Items/([^/]+)/Download
func (a *App) redirectDownload(c *gin.Context) {
	// Extract itemId using regex pattern: /Items/([^/]+)/Download
	// This matches paths like: /Items/12345/Download or /Items/abc-def/Download
	path := c.Request.URL.Path
	lowerPath := strings.ToLower(path)

	// Check if this matches the download pattern
	if !strings.Contains(lowerPath, "/items/") || !strings.Contains(lowerPath, "/download") {
		a.handleEmbyProxy(c)
		return
	}

	// Extract itemId using regex-like logic (matching nginx location ~* /Items/([^/]+)/Download)
	var itemId string
	pathParts := strings.Split(path, "/")
	for i := 0; i < len(pathParts)-2; i++ {
		if pathParts[i] == "Items" && pathParts[i+2] == "Download" {
			itemId = pathParts[i+1]
			break
		}
	}

	// Handle HEAD requests - proxy directly as per emby.conf
	if c.Request.Method == "HEAD" {
		a.handleEmbyProxy(c)
		return
	}

	// Set API type for routing decisions (similar to emby.conf set $apiType "ItemsDownload")
	c.Set("apiType", "ItemsDownload")

	// Check if redirect is allowed using route manager
	action := a.routeManager.EvaluateRequest(c, "", nil)
	if action != "redirect" && action != "transcode" {
		if action == "block" || action == "blockDownload" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Download blocked by routing rules"})
			return
		}
		a.handleEmbyProxy(c)
		return
	}

	if itemId == "" {
		// If we can't extract itemId, try to get it from query parameters
		itemId = c.Query("itemId")
		if itemId == "" {
			a.handleEmbyProxy(c)
			return
		}
	}

	// Fetch Emby file info to get the pickcode
	embyRes, err := a.fetchEmbyFilePath(c, itemId)
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Check if this is a remote/stream file that needs redirect
	if !embyRes.NotLocal {
		// Local file, proxy directly
		a.handleEmbyProxy(c)
		return
	}

	// Extract pickcode from the path
	pickCode := extractPickCodeFromPath(embyRes.Path)
	if pickCode == "" {
		// If no pickcode found, fallback to original link
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no pickcode found in path"})
		return
	}

	// Use the built-in handleDownload logic to get 115直链
	a.mu.Lock()
	client, err := a.ensureClientLocked()
	a.mu.Unlock()
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
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
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if info.Url.Url == "" {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no download url available"})
		return
	}

	// Add cache control headers as per emby.conf: add_header Cache-Control max-age=3600
	c.Header("Cache-Control", "max-age=3600")
	c.Header("Referrer-Policy", "no-referrer")

	// Return 302 redirect to 115直链
	c.Redirect(http.StatusFound, info.Url.Url)
}

// redirectSyncDownload handles Sync/JobItems download requests based on emby.conf location ~* /Sync/JobItems/(.*)/File
func (a *App) redirectSyncDownload(c *gin.Context) {
	// Extract itemId using regex pattern: /Sync/JobItems/(.*)/File
	// This matches paths like: /Sync/JobItems/12345/File
	path := c.Request.URL.Path
	lowerPath := strings.ToLower(path)

	// Check if this matches the sync download pattern
	if !strings.Contains(lowerPath, "/sync/jobitems/") || !strings.Contains(lowerPath, "/file") {
		a.handleEmbyProxy(c)
		return
	}

	// Extract itemId using regex-like logic (matching nginx location ~* /Sync/JobItems/(.*)/File)
	var itemId string
	pathParts := strings.Split(path, "/")
	for i := 0; i < len(pathParts)-2; i++ {
		if pathParts[i] == "Sync" && i+1 < len(pathParts) && pathParts[i+1] == "JobItems" && pathParts[i+2] == "File" {
			itemId = pathParts[i+1]
			break
		}
	}

	// Handle HEAD requests - proxy directly as per emby.conf
	if c.Request.Method == "HEAD" {
		a.handleEmbyProxy(c)
		return
	}

	// Set API type for routing decisions (similar to emby.conf set $apiType "SyncDownload")
	c.Set("apiType", "SyncDownload")

	// Check if redirect is allowed using route manager
	action := a.routeManager.EvaluateRequest(c, "", nil)
	if action != "redirect" && action != "transcode" {
		if action == "block" || action == "blockDownload" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Sync download blocked by routing rules"})
			return
		}
		a.handleEmbyProxy(c)
		return
	}

	if itemId == "" {
		// If we can't extract itemId, try to get it from query parameters
		itemId = c.Query("itemId")
		if itemId == "" {
			a.handleEmbyProxy(c)
			return
		}
	}

	// Fetch Emby file info to get the pickcode
	embyRes, err := a.fetchEmbyFilePath(c, itemId)
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Check if this is a remote/stream file that needs redirect
	if !embyRes.NotLocal {
		// Local file, proxy directly
		a.handleEmbyProxy(c)
		return
	}

	// Extract pickcode from the path
	pickCode := extractPickCodeFromPath(embyRes.Path)
	if pickCode == "" {
		// If no pickcode found, fallback to original link
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no pickcode found in path"})
		return
	}

	// Use the built-in handleDownload logic to get 115直链
	a.mu.Lock()
	client, err := a.ensureClientLocked()
	a.mu.Unlock()
	if err != nil {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
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
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if info.Url.Url == "" {
		if a.cfg.EmbyRedirect.FallbackUseOriginal {
			a.handleEmbyProxy(c)
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no download url available"})
		return
	}

	// Add cache control headers as per emby.conf: add_header Cache-Control max-age=3600
	c.Header("Cache-Control", "max-age=3600")
	c.Header("Referrer-Policy", "no-referrer")

	// Return 302 redirect to 115直链
	c.Redirect(http.StatusFound, info.Url.Url)
}

// handleUsersItems handles Users/Items requests
func (a *App) handleUsersItems(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement items filtering logic
	a.handleEmbyProxy(c)
}

// handleUsersItemsLatest handles Users/Items/Latest requests
func (a *App) handleUsersItemsLatest(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement latest items filtering logic
	a.handleEmbyProxy(c)
}

// handleItemsSimilar handles Items/Similar requests
func (a *App) handleItemsSimilar(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement similar items logic
	a.handleEmbyProxy(c)
}

// handleSystemInfo handles system/info requests
func (a *App) handleSystemInfo(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement system info modification logic
	a.handleEmbyProxy(c)
}

// handleSessionsPlaying handles Sessions/Playing requests
func (a *App) handleSessionsPlaying(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement playing state modification logic
	a.handleEmbyProxy(c)
}

// handleActiveEncodings handles Videos/ActiveEncodings requests
func (a *App) handleActiveEncodings(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement active encodings management logic
	a.handleEmbyProxy(c)
}

// handleVirtualSubtitles handles virtual media subtitle requests
func (a *App) handleVirtualSubtitles(c *gin.Context) {
	// For now, just proxy these requests
	// TODO: Implement virtual subtitle handling logic
	a.handleEmbyProxy(c)
}

// handleWebSocket handles WebSocket connections
func (a *App) handleWebSocket(c *gin.Context) {
	// For now, just proxy WebSocket connections
	// TODO: Implement WebSocket handling logic
	a.handleEmbyProxy(c)
}

func extractPickCodeFromPath(path string) string {
	if !strings.Contains(path, "/d/") {
		return ""
	}

	// Find the "/d/" part
	parts := strings.Split(path, "/d/")
	if len(parts) < 2 {
		return ""
	}

	// Get the part after "/d/"
	afterD := parts[1]

	// Extract pickcode (until ? or end of string)
	if idx := strings.Index(afterD, "?"); idx != -1 {
		return afterD[:idx]
	}

	return afterD
}
