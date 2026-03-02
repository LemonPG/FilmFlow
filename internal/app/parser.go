package app

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mozillazg/go-pinyin"
)

// MediaInfo 存储从文件夹名称中提取的媒体信息
type MediaInfo struct {
	Title    string `json:"Title"`    // 剧名/电影名
	Season   string `json:"season"`   // 季（如 "S01", "S02"）
	Year     string `json:"year"`     // 年份
	IsTVShow bool   `json:"isTVShow"` // 是否是电视剧
	RawName  string `json:"rawName"`  // 原始文件夹名称

	// TMDB 相关字段
	TMDBID         int      `json:"tmdbId,omitempty"`         // TMDB ID
	Overview       string   `json:"overview,omitempty"`       // 概述
	PosterPath     string   `json:"posterPath,omitempty"`     // 海报路径
	BackdropPath   string   `json:"backdropPath,omitempty"`   // 背景图路径
	VoteAverage    float64  `json:"voteAverage,omitempty"`    // 评分
	VoteCount      int      `json:"voteCount,omitempty"`      // 评分人数
	ReleaseDate    string   `json:"releaseDate,omitempty"`    // 上映日期（电影）/首播日期（电视剧）
	Genres         []int    `json:"genres,omitempty"`         // 类型ID
	Popularity     float64  `json:"popularity,omitempty"`     // 流行度
	OriginalTitle  string   `json:"originalTitle,omitempty"`  // 原始标题 英文
	OriginalName   string   `json:"originalName,omitempty"`   // 原始名称（电视剧）原产国语言
	Name           string   `json:"name,omitempty"`           // 名称（电视剧）中文
	PinyinInitials string   `json:"pinyinInitials,omitempty"` // 拼音首字母
	OriginCountry  []string `json:"originCountry,omitempty"`  // 原产国/地区代码（ISO 3166-1 alpha-2）

	TVResult    *TVResult     `json:"tvResult,omitempty"`    // 电视剧搜索结果（如果是电视剧）
	MovieResult *MovieDetails `json:"movieResult,omitempty"` // 电影搜索结果（如果是电影）
}

// ParseFolderName 解析文件夹名称，提取媒体信息
func ParseFolderName(folderName string) (*MediaInfo, error) {
	info := &MediaInfo{
		RawName: folderName,
	}

	// 清理文件夹名称：替换点、下划线、连字符为空格
	cleaned := strings.ReplaceAll(folderName, ".", " ")
	cleaned = strings.ReplaceAll(cleaned, "_", " ")
	cleaned = strings.ReplaceAll(cleaned, "-", " ")

	// 移除多余的空格
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	// 尝试匹配电视剧格式（包含季信息）
	if tvInfo := parseTVShow(cleaned); tvInfo != nil {
		// 复制所有字段
		info.Title = tvInfo.Title
		info.Season = tvInfo.Season
		info.Year = tvInfo.Year
		info.IsTVShow = true
		info.OriginalTitle = tvInfo.OriginalTitle
		info.OriginalName = tvInfo.OriginalName
		return info, nil
	}

	// 尝试匹配电影格式
	if movieInfo := parseMovie(cleaned); movieInfo != nil {
		// 复制所有字段
		info.Title = movieInfo.Title
		info.Year = movieInfo.Year
		info.IsTVShow = false
		info.OriginalTitle = movieInfo.OriginalTitle
		info.OriginalName = movieInfo.OriginalName
		return info, nil
	}

	// 如果无法解析，返回原始名称作为标题
	info.Title = folderName
	return info, nil
}

// parseTVShow 解析电视剧文件夹名称
func parseTVShow(name string) *MediaInfo {
	// 正则表达式匹配电视剧格式
	// 格式1: 剧名 S01 2025 ... (如 "Taxi Driver S03 2025")
	// 格式2: 剧名.S01.2025... (如 "Rite.of.Passage.S01.2025")
	// 格式3: 剧名 S01E01 2025 ... (处理季信息)

	// 匹配季信息 (S01, S02, S1, S2, Season 1, Season1)
	seasonRegex := regexp.MustCompile(`(?i)(?:S|Season\s*)(\d{1,2})`)
	yearRegex := regexp.MustCompile(`\b(19|20)\d{2}\b`)

	// 查找季信息
	seasonMatch := seasonRegex.FindStringSubmatch(name)
	if seasonMatch == nil {
		return nil // 没有季信息，可能不是电视剧
	}

	season := fmt.Sprintf("S%02s", seasonMatch[1])

	// 查找年份
	var year string
	yearMatch := yearRegex.FindString(name)
	if yearMatch != "" {
		year = yearMatch
	}

	// 提取标题：从开头到季信息之前的部分
	seasonIndex := seasonRegex.FindStringIndex(name)
	if seasonIndex == nil {
		return nil
	}

	title := strings.TrimSpace(name[:seasonIndex[0]])

	// 清理标题：移除常见的质量标识符
	title = cleanTitle(title)

	// 分离中英文标题
	chineseTitle, englishTitle := splitChineseEnglishTitle(title)

	// 设置标题：优先使用中文标题，如果没有中文则使用原始标题
	finalTitle := title
	if chineseTitle != "" {
		finalTitle = chineseTitle
	}

	mediaInfo := &MediaInfo{
		Title:    finalTitle,
		Season:   season,
		Year:     year,
		IsTVShow: true,
	}

	// 如果存在英文标题，且与中文标题不同，则存储在OriginalTitle字段
	if englishTitle != "" && englishTitle != chineseTitle {
		mediaInfo.OriginalTitle = englishTitle
	}

	return mediaInfo
}

// parseMovie 解析电影文件夹名称
func parseMovie(name string) *MediaInfo {
	// 正则表达式匹配电影格式
	// 格式: 电影名 年份 ... (如 "New Shaolin Boxers 1976")
	// 或: 电影名.年份... (如 "New.Shaolin.Boxers.1976")

	yearRegex := regexp.MustCompile(`\b(19|20)\d{2}\b`)
	yearMatch := yearRegex.FindString(name)

	if yearMatch == "" {
		return nil // 没有年份信息，可能无法识别为电影
	}

	// 查找年份位置
	yearIndex := yearRegex.FindStringIndex(name)
	if yearIndex == nil {
		return nil
	}

	// 提取标题：从开头到年份之前的部分
	title := strings.TrimSpace(name[:yearIndex[0]])

	// 清理标题：移除常见的质量标识符
	title = cleanTitle(title)

	// 分离中英文标题
	chineseTitle, englishTitle := splitChineseEnglishTitle(title)

	// 设置标题：优先使用中文标题，如果没有中文则使用原始标题
	finalTitle := title
	if chineseTitle != "" {
		finalTitle = chineseTitle
	}

	mediaInfo := &MediaInfo{
		Title:    finalTitle,
		Year:     yearMatch,
		IsTVShow: false,
	}

	// 如果存在英文标题，且与中文标题不同，则存储在OriginalTitle字段
	if englishTitle != "" && englishTitle != chineseTitle {
		mediaInfo.OriginalTitle = englishTitle
	}

	return mediaInfo
}

// cleanTitle 清理标题，移除常见的质量标识符
func cleanTitle(title string) string {
	// 常见的视频质量标识符
	qualityPatterns := []string{
		"1080p", "720p", "2160p", "4K", "UHD", "HDTV", "BluRay", "WEB-DL", "WEBRip",
		"x264", "x265", "H264", "H265", "HEVC", "AVC", "DDP5.1", "AC3", "AAC", "FLAC",
		"MPEG2", "NF", "CR", "AMZN", "ATVP", "DSNP", "HMAX", "HULU", "iNTERNAL",
		"REPACK", "PROPER", "READNFO", "RARBG", "YTS", "EVO", "TAoE", "CtrlHD",
	}

	// 移除发布组信息（通常在最后，以-或@开头）
	releaseGroupRegex := regexp.MustCompile(`[-@][A-Za-z0-9]+(?:\.[A-Za-z0-9]+)*$`)
	title = releaseGroupRegex.ReplaceAllString(title, "")

	// 移除质量标识符
	for _, pattern := range qualityPatterns {
		// 创建不区分大小写的正则表达式
		re := regexp.MustCompile(`(?i)\s*` + regexp.QuoteMeta(pattern) + `\s*`)
		title = re.ReplaceAllString(title, " ")
	}

	// 清理多余的空格和标点
	title = strings.TrimSpace(title)
	title = strings.Trim(title, ".-_ ")

	return title
}

// splitChineseEnglishTitle 分离中英文标题
func splitChineseEnglishTitle(title string) (chineseTitle, englishTitle string) {
	// 使用正则表达式匹配中文和英文部分
	chineseRegex := regexp.MustCompile(`[\p{Han}]+`)
	englishRegex := regexp.MustCompile(`[A-Za-z\s']+`)

	// 查找所有中文部分
	chineseMatches := chineseRegex.FindAllString(title, -1)
	// 查找所有英文部分
	englishMatches := englishRegex.FindAllString(title, -1)

	// 合并中文部分
	if len(chineseMatches) > 0 {
		chineseTitle = strings.Join(chineseMatches, " ")
		chineseTitle = strings.TrimSpace(chineseTitle)
	}

	// 合并英文部分
	if len(englishMatches) > 0 {
		englishTitle = strings.Join(englishMatches, " ")
		englishTitle = strings.TrimSpace(englishTitle)
		// 清理英文标题中的多余空格
		englishTitle = strings.Join(strings.Fields(englishTitle), " ")
	}

	return chineseTitle, englishTitle
}

// String 返回MediaInfo的字符串表示
func (m *MediaInfo) String() string {
	if m.IsTVShow {
		if m.Year != "" {
			return fmt.Sprintf("%s %s (%s)", m.Title, m.Season, m.Year)
		}
		return fmt.Sprintf("%s %s", m.Title, m.Season)
	}
	if m.Year != "" {
		return fmt.Sprintf("%s (%s)", m.Title, m.Year)
	}
	return m.Title
}

// 打印全部信息
func (m *MediaInfo) AllInfo() string {
	var sb strings.Builder

	writeField := func(name string, value interface{}) {
		sb.WriteString(fmt.Sprintf("%s: %v\n", name, value))
	}

	writeField("Title", m.Title)
	writeField("Season", m.Season)
	writeField("Year", m.Year)
	writeField("Is TV Show", m.IsTVShow)
	writeField("Raw Name", m.RawName)
	writeField("TMDB ID", m.TMDBID)
	writeField("Overview", m.Overview)
	writeField("Poster Path", m.PosterPath)
	writeField("Backdrop Path", m.BackdropPath)
	writeField("Vote Average", fmt.Sprintf("%.1f", m.VoteAverage))
	writeField("Vote Count", m.VoteCount)
	writeField("Release Date", m.ReleaseDate)
	writeField("Genres", m.Genres)
	writeField("Popularity", fmt.Sprintf("%.2f", m.Popularity))
	writeField("Original Title", m.OriginalTitle)
	writeField("Original Name", m.OriginalName)
	writeField("Pinyin Initials", m.PinyinInitials)
	writeField("Origin Country", m.OriginCountry)

	if m.IsTVShow {
		writeField("Adult", m.TVResult.Adult)
		writeField("Backdrop Path", m.TVResult.BackdropPath)
		writeField("Genre IDs", m.TVResult.GenreIDs)
		writeField("ID", m.TVResult.ID)
		writeField("Origin Country", m.TVResult.OriginCountry)
		writeField("Original Language", m.TVResult.OriginalLanguage)
		writeField("Original Name", m.TVResult.OriginalName)
		writeField("Overview", m.TVResult.Overview)
		writeField("Popularity", fmt.Sprintf("%.2f", m.TVResult.Popularity))
		writeField("Poster Path", m.TVResult.PosterPath)
		writeField("First Air Date", m.TVResult.FirstAirDate)
		writeField("Name", m.TVResult.Name)
		writeField("Vote Average", fmt.Sprintf("%.1f", m.TVResult.VoteAverage))
		writeField("Vote Count", m.TVResult.VoteCount)
	} else {
		writeField("Adult", m.MovieResult.Adult)
		writeField("Backdrop Path", m.MovieResult.BackdropPath)
		writeField("Genre IDs", m.MovieResult.GenreIDs)
		writeField("ID", m.MovieResult.ID)
		writeField("Original Language", m.MovieResult.OriginalLanguage)
		writeField("Original Title", m.MovieResult.OriginalTitle)
		writeField("Overview", m.MovieResult.Overview)
		writeField("Popularity", fmt.Sprintf("%.2f", m.MovieResult.Popularity))
		writeField("Poster Path", m.MovieResult.PosterPath)
		writeField("Release Date", m.MovieResult.ReleaseDate)
		writeField("Title", m.MovieResult.Title)
		writeField("Video", m.MovieResult.Video)
		writeField("Vote Average", fmt.Sprintf("%.1f", m.MovieResult.VoteAverage))
		writeField("Vote Count", m.MovieResult.VoteCount)
	}

	return sb.String()
}

// ToDetailedString 返回详细的媒体信息字符串
func (m *MediaInfo) ToDetailedString() string {
	base := m.String()
	if m.TMDBID > 0 {
		base = fmt.Sprintf("%s [TMDB ID: %d]", base, m.TMDBID)
		if m.VoteAverage > 0 {
			base = fmt.Sprintf("%s ⭐%.1f", base, m.VoteAverage)
		}
	}
	return base
}

// String 返回MediaInfo的字符串表示
func (m *MediaInfo) YearInt() int {
	if m.Year != "" {
		year, err := strconv.Atoi(m.Year)
		if err != nil {
			return 0
		}
		return year
	}
	return 0
}

// GetPinyinInitials 获取字符串的拼音首字母
// 使用 github.com/mozillazg/go-pinyin 库实现
func GetPinyinInitials(s string) string {
	if s == "" {
		return ""
	}

	// 配置拼音转换参数
	args := pinyin.NewArgs()
	args.Style = pinyin.FirstLetter

	// 获取拼音首字母
	pinyinSlice := pinyin.Pinyin(s, args)

	// 查找第一个有效拼音首字母
	for _, py := range pinyinSlice {
		if len(py) > 0 && py[0] != "" {
			// 转为大写
			return strings.ToUpper(py[0][:1])
		}
	}

	return ""
}

// 写一个返回季的字符串Season xx
func (m *MediaInfo) SeasonString() string {
	if m.Season != "" {
		return fmt.Sprintf("Season %s", m.Season[1:]) // 去掉"S"前缀
	}
	return ""
}
