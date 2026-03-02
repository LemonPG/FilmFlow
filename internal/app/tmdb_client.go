package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TMDBClient 是 themoviedb.org API 客户端
type TMDBClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// TMDBConfig TMDB 配置
type TMDBConfig struct {
	APIKey string `json:"apiKey"`
}

// MovieSearchResult 电影搜索结果
type MovieSearchResult struct {
	Page         int           `json:"page"`
	Results      []MovieResult `json:"results"`
	TotalPages   int           `json:"total_pages"`
	TotalResults int           `json:"total_results"`
}

// TVSearchResult 电视剧搜索结果
type TVSearchResult struct {
	Page         int        `json:"page"`
	Results      []TVResult `json:"results"`
	TotalPages   int        `json:"total_pages"`
	TotalResults int        `json:"total_results"`
}

// MovieResult 电影结果
type MovieResult struct {
	Adult            bool    `json:"adult"`
	BackdropPath     string  `json:"backdrop_path"`
	GenreIDs         []int   `json:"genre_ids"`
	ID               int     `json:"id"`
	OriginalLanguage string  `json:"original_language"`
	OriginalTitle    string  `json:"original_title"`
	Overview         string  `json:"overview"`
	Popularity       float64 `json:"popularity"`
	PosterPath       string  `json:"poster_path"`
	ReleaseDate      string  `json:"release_date"`
	Title            string  `json:"title"`
	Video            bool    `json:"video"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
}

// MovieDetails 电影详情（包含生产国家等信息）
type MovieDetails struct {
	MovieResult
	ProductionCountries []ProductionCountry `json:"production_countries"`
}

// ProductionCountry 生产国家
type ProductionCountry struct {
	ISO3166_1 string `json:"iso_3166_1"`
	Name      string `json:"name"`
}

// TVResult 电视剧结果
type TVResult struct {
	Adult            bool     `json:"adult"`
	BackdropPath     string   `json:"backdrop_path"`
	GenreIDs         []int    `json:"genre_ids"`
	ID               int      `json:"id"`
	OriginCountry    []string `json:"origin_country"`
	OriginalLanguage string   `json:"original_language"`
	OriginalName     string   `json:"original_name"`
	Overview         string   `json:"overview"`
	Popularity       float64  `json:"popularity"`
	PosterPath       string   `json:"poster_path"`
	FirstAirDate     string   `json:"first_air_date"`
	Name             string   `json:"name"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int      `json:"vote_count"`
}

// SearchMovieRequest 电影搜索请求参数
type SearchMovieRequest struct {
	Query        string `json:"query"`
	IncludeAdult bool   `json:"include_adult"`
	Language     string `json:"language"`
	Page         int    `json:"page"`
	Year         int    `json:"year"`
	Region       string `json:"region"`
}

// SearchTVRequest 电视剧搜索请求参数
type SearchTVRequest struct {
	Query        string `json:"query"`
	IncludeAdult bool   `json:"include_adult"`
	Language     string `json:"language"`
	Page         int    `json:"page"`
	Year         int    `json:"year"`
}

// NewTMDBClient 创建新的 TMDB 客户端
func NewTMDBClient(apiKey string) *TMDBClient {
	return &TMDBClient{
		apiKey:  apiKey,
		baseURL: "https://api.themoviedb.org/3",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SearchMovies 搜索电影
func (c *TMDBClient) SearchMovies(req SearchMovieRequest) (*MovieSearchResult, error) {
	// 构建 URL
	u, err := url.Parse(fmt.Sprintf("%s/search/movie", c.baseURL))
	if err != nil {
		return nil, fmt.Errorf("解析URL失败: %v", err)
	}

	// 构建查询参数
	q := u.Query()
	q.Set("query", req.Query)
	q.Set("include_adult", fmt.Sprintf("%v", req.IncludeAdult))
	q.Set("language", req.Language)
	q.Set("page", fmt.Sprintf("%d", req.Page))
	if req.Year > 0 {
		q.Set("year", fmt.Sprintf("%d", req.Year))
	}
	if req.Region != "" {
		q.Set("region", req.Region)
	}
	u.RawQuery = q.Encode()

	// 创建请求
	httpReq, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 设置认证头
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	httpReq.Header.Set("accept", "application/json")

	// 发送请求
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API请求失败: %s", resp.Status)
	}

	// 解析响应
	var result MovieSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	return &result, nil
}

// SearchTV 搜索电视剧
func (c *TMDBClient) SearchTV(req SearchTVRequest) (*TVSearchResult, error) {
	// 构建 URL
	u, err := url.Parse(fmt.Sprintf("%s/search/tv", c.baseURL))
	if err != nil {
		return nil, fmt.Errorf("解析URL失败: %v", err)
	}

	// 构建查询参数
	q := u.Query()
	q.Set("query", req.Query)
	q.Set("include_adult", fmt.Sprintf("%v", req.IncludeAdult))
	q.Set("language", req.Language)
	q.Set("page", fmt.Sprintf("%d", req.Page))
	if req.Year > 0 {
		q.Set("year", fmt.Sprintf("%d", req.Year))
	}
	u.RawQuery = q.Encode()

	// 创建请求
	httpReq, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 设置认证头
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	httpReq.Header.Set("accept", "application/json")

	// 发送请求
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API请求失败: %s", resp.Status)
	}

	// 解析响应
	var result TVSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	return &result, nil
}

// SearchMoviesWithQuery 使用查询字符串搜索电影（简化版）
func (c *TMDBClient) SearchMoviesWithQuery(query string, year int, language string, page int) (*MovieSearchResult, error) {
	req := SearchMovieRequest{
		Query:        query,
		Year:         year,
		IncludeAdult: false,
		Language:     language,
		Page:         page,
	}
	return c.SearchMovies(req)
}

// SearchTVWithQuery 使用查询字符串搜索电视剧（简化版）
func (c *TMDBClient) SearchTVWithQuery(query string, year int, language string, page int) (*TVSearchResult, error) {
	req := SearchTVRequest{
		Query:        query,
		Year:         year,
		IncludeAdult: false,
		Language:     language,
		Page:         page,
	}
	return c.SearchTV(req)
}

// GetMovieDetails 获取电影详情
func (c *TMDBClient) GetMovieDetails(movieID int, language string) (*MovieDetails, error) {
	// 构建 URL
	u, err := url.Parse(fmt.Sprintf("%s/movie/%d", c.baseURL, movieID))
	if err != nil {
		return nil, fmt.Errorf("解析URL失败: %v", err)
	}

	// 构建查询参数
	q := u.Query()
	if language != "" {
		q.Set("language", language)
	}
	u.RawQuery = q.Encode()

	// 创建请求
	httpReq, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 设置认证头
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	httpReq.Header.Set("accept", "application/json")

	// 发送请求
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API请求失败: %s", resp.Status)
	}

	// 解析响应
	var result MovieDetails
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	return &result, nil
}

// GetTVDetails 获取电视剧详情
func (c *TMDBClient) GetTVDetails(tvID int, language string) (*TVResult, error) {
	// 构建 URL
	u, err := url.Parse(fmt.Sprintf("%s/tv/%d", c.baseURL, tvID))
	if err != nil {
		return nil, fmt.Errorf("解析URL失败: %v", err)
	}

	// 构建查询参数
	q := u.Query()
	if language != "" {
		q.Set("language", language)
	}
	u.RawQuery = q.Encode()

	// 创建请求
	httpReq, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 设置认证头
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	httpReq.Header.Set("accept", "application/json")

	// 发送请求
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API请求失败: %s", resp.Status)
	}

	// 解析响应
	var result TVResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	return &result, nil
}

// FormatPosterURL 格式化海报URL
func (c *TMDBClient) FormatPosterURL(path string, size string) string {
	if path == "" {
		return ""
	}
	// 移除开头的斜杠
	path = strings.TrimPrefix(path, "/")
	return fmt.Sprintf("https://image.tmdb.org/t/p/%s/%s", size, path)
}

// FormatBackdropURL 格式化背景图URL
func (c *TMDBClient) FormatBackdropURL(path string, size string) string {
	if path == "" {
		return ""
	}
	// 移除开头的斜杠
	path = strings.TrimPrefix(path, "/")
	return fmt.Sprintf("https://image.tmdb.org/t/p/%s/%s", size, path)
}
