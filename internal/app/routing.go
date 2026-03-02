package app

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RouteRule represents a routing rule
type RouteRule struct {
	Action   string `json:"action"`   // "redirect", "proxy", "transcode", "block", "blockDownload", "blockPlay"
	Group    string `json:"group"`    // Group name for AND relationships
	Type     string `json:"type"`     // "filePath", "userAgent", "deviceId", etc.
	Match    string `json:"match"`    // "startsWith", "endsWith", "includes", "regex"
	Pattern  string `json:"pattern"`  // Pattern to match
	Priority int    `json:"priority"` // Rule priority (higher = more important)
}

// RouteCache represents a cached route decision
type RouteCache struct {
	Action    string
	ExpiresAt time.Time
}

// RouteManager manages routing rules and caching
type RouteManager struct {
	mu     sync.RWMutex
	rules  []RouteRule
	cache  map[string]*RouteCache
	config *EmbyRedirectConfig
}

// NewRouteManager creates a new route manager
func NewRouteManager(config *EmbyRedirectConfig) *RouteManager {
	return &RouteManager{
		rules:  make([]RouteRule, 0),
		cache:  make(map[string]*RouteCache),
		config: config,
	}
}

// AddRule adds a new routing rule
func (rm *RouteManager) AddRule(rule RouteRule) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.rules = append(rm.rules, rule)
}

// EvaluateRequest evaluates a request against routing rules
func (rm *RouteManager) EvaluateRequest(c *gin.Context, filePath string, mediaSource *MediaSource) string {
	// Check cache first if enabled
	if rm.config.RouteCacheEnable {
		cacheKey := rm.generateCacheKey(c, filePath, mediaSource)
		if action := rm.getFromCache(cacheKey); action != "" {
			return action
		}
	}

	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Default action is redirect if no rules match
	defaultAction := "redirect"

	// Evaluate rules in priority order
	for _, rule := range rm.rules {
		if rm.matchRule(c, filePath, mediaSource, rule) {
			action := rule.Action
			// Cache the result if enabled
			if rm.config.RouteCacheEnable {
				rm.cacheResult(rm.generateCacheKey(c, filePath, mediaSource), action)
			}
			return action
		}
	}

	return defaultAction
}

// matchRule checks if a request matches a routing rule
func (rm *RouteManager) matchRule(c *gin.Context, filePath string, mediaSource *MediaSource, rule RouteRule) bool {
	var value string

	switch rule.Type {
	case "filePath":
		value = filePath
	case "userAgent":
		value = c.GetHeader("User-Agent")
	case "deviceId":
		value = c.Query("X-Emby-Device-Id")
	case "deviceName":
		value = c.Query("X-Emby-Device-Name")
	case "userId":
		value = c.Query("UserId")
	case "mediaSource":
		if mediaSource != nil {
			switch rule.Pattern {
			case "isRemote":
				value = formatBool(mediaSource.IsRemote)
			case "isInfiniteStream":
				value = formatBool(mediaSource.IsInfiniteStream)
			case "bitrate":
				value = string(rune(mediaSource.Bitrate))
			default:
				value = mediaSource.Name
			}
		}
	default:
		return false
	}

	switch rule.Match {
	case "startsWith":
		return strings.HasPrefix(value, rule.Pattern)
	case "endsWith":
		return strings.HasSuffix(value, rule.Pattern)
	case "includes":
		return strings.Contains(value, rule.Pattern)
	case "regex":
		// TODO: Implement regex matching
		return false
	default:
		return false
	}
}

// generateCacheKey generates a cache key for the request
func (rm *RouteManager) generateCacheKey(c *gin.Context, filePath string, mediaSource *MediaSource) string {
	key := c.Request.URL.Path
	if rm.config.RouteCacheL2Enable {
		key += ":" + c.GetHeader("User-Agent")
		if mediaSource != nil {
			key += ":" + mediaSource.ID
		}
	}
	return key
}

// getFromCache retrieves an action from cache
func (rm *RouteManager) getFromCache(key string) string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if cached, exists := rm.cache[key]; exists {
		if time.Now().Before(cached.ExpiresAt) {
			return cached.Action
		}
		// Remove expired cache entry
		delete(rm.cache, key)
	}
	return ""
}

// cacheResult caches a routing decision
func (rm *RouteManager) cacheResult(key, action string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Cache for 5 minutes
	rm.cache[key] = &RouteCache{
		Action:    action,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	// Clean up expired entries periodically
	if len(rm.cache) > 1000 {
		rm.cleanupCache()
	}
}

// cleanupCache removes expired cache entries
func (rm *RouteManager) cleanupCache() {
	now := time.Now()
	for key, cached := range rm.cache {
		if now.After(cached.ExpiresAt) {
			delete(rm.cache, key)
		}
	}
}

// formatBool converts boolean to string
func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
