package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/LemonPG/115driver/pkg/driver"
)

// PendingScanner 待整理目录扫描器
type PendingScanner struct {
	app *App
}

// NewPendingScanner 创建新的待整理目录扫描器
func NewPendingScanner(app *App) *PendingScanner {
	return &PendingScanner{
		app: app,
	}
}

// ScanPendingDirectory 扫描待整理目录，提取文件夹信息
func (ps *PendingScanner) ScanPendingDirectory() ([]*MediaInfo, error) {
	ps.app.mu.Lock()
	cfg := ps.app.cfg
	client, err := ps.app.ensureClientLocked()
	ps.app.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("无法获取客户端: %v", err)
	}

	if cfg.PendingCID == "" {
		return nil, fmt.Errorf("待整理目录CID未配置")
	}

	// 获取待整理目录中的文件夹列表
	folders, err := ps.app.rateLimitedList(client, cfg.PendingCID)
	if err != nil {
		return nil, fmt.Errorf("获取目录列表失败: %v", err)
	}

	var mediaInfos []*MediaInfo

	// 遍历所有文件夹，解析文件夹名称
	for _, folder := range *folders {
		if !folder.IsDirectory {
			continue // 只处理文件夹
		}

		// 解析文件夹名称
		mediaInfo, err := ParseFolderName(folder.Name)
		if err != nil {
			log.Printf("解析文件夹名称失败: %s, 错误: %v", folder.Name, err)
			continue
		}

		if mediaInfo.Year == "" {
			log.Printf("文件夹名称缺少年份信息: %s", folder.Name)
			continue
		}

		// 添加文件夹CID信息
		mediaInfo.RawName = folder.Name

		// 查询TMDB获取详细信息
		if err := ps.queryTMDBInfo(mediaInfo); err != nil {
			log.Printf("查询TMDB信息失败: %s, 错误: %v", mediaInfo.String(), err)
		}

		// 记录解析结果
		log.Printf("解析文件夹: %s -> %s", folder.Name, mediaInfo.ToDetailedString())

		mediaInfos = append(mediaInfos, mediaInfo)
	}

	log.Printf("待整理目录扫描完成，共找到 %d 个媒体文件夹", len(mediaInfos))
	return mediaInfos, nil
}

// StartAutoScan 启动自动扫描待整理目录
func (ps *PendingScanner) StartAutoScan(interval time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("待整理目录自动扫描已停止")
				return
			case <-ticker.C:
				log.Printf("开始定时扫描待整理目录...")
				ps.scanAndProcess()
			}
		}
	}()

	log.Printf("待整理目录自动扫描已启动，扫描间隔: %v", interval)
	return cancel
}

// scanAndProcess 扫描并处理待整理目录
func (ps *PendingScanner) scanAndProcess() {
	mediaInfos, err := ps.ScanPendingDirectory()
	if err != nil {
		log.Printf("扫描待整理目录失败: %v", err)
		return
	}

	// 处理扫描结果
	ps.processMediaInfos(mediaInfos)
}

// processMediaInfos 处理扫描到的媒体信息
func (ps *PendingScanner) processMediaInfos(mediaInfos []*MediaInfo) {
	ps.app.mu.Lock()
	cfg := ps.app.cfg
	client, err := ps.app.ensureClientLocked()
	ps.app.mu.Unlock()

	if err != nil {
		log.Printf("处理媒体信息时无法获取客户端: %v", err)
		return
	}

	// 获取待整理目录中的文件夹列表
	folders, err := ps.app.rateLimitedList(client, cfg.PendingCID)
	if err != nil {
		log.Printf("获取待整理目录列表失败: %v", err)
		return
	}

	// 创建文件夹名称到文件夹信息的映射
	folderMap := make(map[string]driver.File)
	for _, folder := range *folders {
		if folder.IsDirectory {
			folderMap[folder.Name] = folder
		}
	}

	// 处理每个媒体信息
	for _, mediaInfo := range mediaInfos {
		// 查找对应的文件夹
		folder, exists := folderMap[mediaInfo.RawName]
		if !exists {
			log.Printf("文件夹不存在: %s", mediaInfo.RawName)
			continue
		}

		// 确定目标目录
		var targetCID string
		var targetFolderName string

		// 如果没有查询到TMDB信息，移动到冗余目录
		if mediaInfo.TMDBID == 0 {
			if cfg.RedundantCID == "" {
				log.Printf("冗余目录未配置，跳过文件夹: %s", mediaInfo.String())
				continue
			}
			targetCID = cfg.RedundantCID
			targetFolderName = mediaInfo.RawName
			log.Printf("未查询到TMDB信息，移动文件夹到冗余目录: %s -> %s", mediaInfo.String(), targetCID)
		} else {
			// 查询到TMDB信息，按类型和地区分类
			if cfg.SelectedCID == "" {
				log.Printf("目标目录未配置，跳过文件夹: %s", mediaInfo.String())
				continue
			}

			// 获取地区分类
			region := ps.getTargetRegion(mediaInfo)
			if region == "" {
				// 如果没有地区信息，使用"其他地区"
				region = "其他地区"
			}

			// 判断是否是动漫（动画类型ID为16）
			isAnime := false
			for _, genreID := range mediaInfo.Genres {
				if genreID == 16 { // TMDB动画类型ID
					isAnime = true
					break
				}
			}

			// 构建目标目录路径
			var folderName string
			if isAnime {
				// 动漫分类
				if mediaInfo.IsTVShow {
					// 动漫/动画/地区/PinyinInitials/Name(year)/Season xx/原始文件夹
					if mediaInfo.Year != "" {
						folderName = fmt.Sprintf("动漫/动画/%s/%s/%s(%s)/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.Name),
							mediaInfo.Year,
							mediaInfo.SeasonString())
					} else {
						folderName = fmt.Sprintf("动漫/动画/%s/%s/%s/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.Name), mediaInfo.SeasonString())
					}
				} else {
					// 动漫/剧场版/地区/PinyinInitials/Name(year)/Season xx/原始文件夹
					if mediaInfo.Year != "" {
						folderName = fmt.Sprintf("动漫/剧场版/%s/%s/%s(%s)/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.OriginalTitle),
							mediaInfo.Year, mediaInfo.SeasonString())
					} else {
						folderName = fmt.Sprintf("动漫/剧场版/%s/%s/%s/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.OriginalTitle), mediaInfo.SeasonString())
					}
				}
			} else {
				// 真人影视分类
				if mediaInfo.IsTVShow {
					// 真人影视/电视剧/地区/PinyinInitials/Name(year)/Season xx/原始文件夹
					if mediaInfo.Year != "" {
						folderName = fmt.Sprintf("真人影视/电视剧/%s/%s/%s(%s)/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.Name),
							mediaInfo.Year, mediaInfo.SeasonString())
					} else {
						folderName = fmt.Sprintf("真人影视/电视剧/%s/%s/%s/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.Name), mediaInfo.SeasonString())
					}
				} else {
					// 真人影视/电影/地区/PinyinInitials/Name(year)/Season xx/原始文件夹
					if mediaInfo.Year != "" {
						folderName = fmt.Sprintf("真人影视/电影/%s/%s/%s(%s)/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.OriginalTitle),
							mediaInfo.Year, mediaInfo.SeasonString())
					} else {
						folderName = fmt.Sprintf("真人影视/电影/%s/%s/%s/%s",
							region,
							mediaInfo.PinyinInitials,
							cleanFolderName(mediaInfo.OriginalTitle), mediaInfo.SeasonString())
					}
				}
			}
			//根目录加一个emby
			folderName = "Emby/" + folderName
			targetFolderName = folderName

			// 在目标目录中创建文件夹
			targetCID, err = ps.createFolderInSelectedCID(client, cfg.SelectedCID, folderName)
			if err != nil {
				log.Printf("创建目标文件夹失败: %s, 错误: %v", folderName, err)
				continue
			}

			log.Printf("按类型和地区分类移动文件夹: =====\n%s\n===== -> %s", mediaInfo.AllInfo(), folderName)
		}

		// 检查目标文件夹是否已存在（通过检查existingCID目录）
		existingFolderCID, exists := ps.findExistingFolder(client, targetCID, targetFolderName, mediaInfo.RawName)
		if exists {
			// 目标文件夹已存在，进行文件对比和移动
			log.Printf("目标文件夹已存在，进行文件对比和移动: %s -> %s", mediaInfo.RawName, existingFolderCID)
			if err := ps.moveMissingFiles(client, folder.FileID, existingFolderCID); err != nil {
				log.Printf("移动缺少的文件失败: %s -> %s, 错误: %v",
					mediaInfo.RawName, existingFolderCID, err)
				continue
			}
			log.Printf("成功移动缺少的文件到已存在目录: %s -> %s", mediaInfo.RawName, existingFolderCID)

			if err := ps.moveFolder(client, folder.FileID, cfg.ExistingCID); err != nil {
				log.Printf("移动文件夹失败: %s -> %s, 错误: %v",
					mediaInfo.RawName, cfg.ExistingCID, err)
			}
		} else {
			// 目标文件夹不存在，移动整个文件夹
			if err := ps.moveFolder(client, folder.FileID, targetCID); err != nil {
				log.Printf("移动文件夹失败: %s -> %s, 错误: %v",
					mediaInfo.String(), targetCID, err)
				continue
			}
			log.Printf("成功移动文件夹: %s -> 目录: %s", mediaInfo.RawName, targetCID)
		}
	}
}

// moveFolder 移动文件夹到目标目录
func (ps *PendingScanner) moveFolder(client *driver.Pan115Client, folderCID, targetCID string) error {
	// 使用115网盘的移动API
	return ps.app.rateLimitedMove(client, targetCID, folderCID)
}

// renameFolder 重命名文件夹
func (ps *PendingScanner) renameFolder(client *driver.Pan115Client, folderCID, newName string) error {
	// 使用115网盘的重命名API
	return ps.app.rateLimitedRename(client, folderCID, newName)
}

// queryTMDBInfo 查询TMDB获取媒体详细信息
func (ps *PendingScanner) queryTMDBInfo(mediaInfo *MediaInfo) error {
	// 检查TMDB客户端是否可用
	ps.app.mu.Lock()
	tmdbClient := ps.app.tmdbClient
	ps.app.mu.Unlock()

	if tmdbClient == nil {
		return fmt.Errorf("TMDB客户端未初始化，请检查API密钥配置")
	}

	// 根据媒体类型进行查询
	if mediaInfo.IsTVShow {
		return ps.queryTVShowInfo(tmdbClient, mediaInfo)
	} else {
		return ps.queryMovieInfo(tmdbClient, mediaInfo)
	}
}

// queryTVShowInfo 查询电视剧信息
func (ps *PendingScanner) queryTVShowInfo(tmdbClient *TMDBClient, mediaInfo *MediaInfo) error {
	// 搜索电视剧
	result, err := tmdbClient.SearchTVWithQuery(mediaInfo.Title, mediaInfo.YearInt(), "zh-CN", 1)
	if err != nil {
		return fmt.Errorf("搜索电视剧失败: %v", err)
	}

	if len(result.Results) == 0 {
		return fmt.Errorf("未找到匹配的电视剧: %s", mediaInfo.Title)
	}

	// 取第一个结果（通常是最匹配的）
	tvResult := result.Results[0]

	// 填充媒体信息
	mediaInfo.TMDBID = tvResult.ID
	mediaInfo.Overview = tvResult.Overview
	mediaInfo.PosterPath = tvResult.PosterPath
	mediaInfo.BackdropPath = tvResult.BackdropPath
	mediaInfo.VoteAverage = tvResult.VoteAverage
	mediaInfo.VoteCount = tvResult.VoteCount
	mediaInfo.ReleaseDate = tvResult.FirstAirDate
	mediaInfo.Genres = tvResult.GenreIDs
	mediaInfo.Popularity = tvResult.Popularity
	mediaInfo.OriginalName = tvResult.OriginalName
	mediaInfo.Name = tvResult.Name
	mediaInfo.OriginCountry = tvResult.OriginCountry

	// 可选：获取更详细的电视剧信息
	if details, err := tmdbClient.GetTVDetails(tvResult.ID, "zh-CN"); err == nil {
		mediaInfo.TVResult = details
		// 如果详情中有OriginCountry，使用详情中的（可能更准确）
		if len(details.OriginCountry) > 0 {
			mediaInfo.OriginCountry = details.OriginCountry
		}
	}

	// 设置拼音首字母
	mediaInfo.PinyinInitials = GetPinyinInitials(mediaInfo.Name)
	if mediaInfo.PinyinInitials == "" {
		// 如果Name为空，使用Title
		mediaInfo.PinyinInitials = GetPinyinInitials(mediaInfo.Title)
	}

	return nil
}

// queryMovieInfo 查询电影信息
func (ps *PendingScanner) queryMovieInfo(tmdbClient *TMDBClient, mediaInfo *MediaInfo) error {
	// 搜索电影
	result, err := tmdbClient.SearchMoviesWithQuery(mediaInfo.Title, mediaInfo.YearInt(), "zh-CN", 1)
	if err != nil {
		return fmt.Errorf("搜索电影失败: %v", err)
	}

	if len(result.Results) == 0 {
		return fmt.Errorf("未找到匹配的电影: %s", mediaInfo.Title)
	}

	// 取第一个结果（通常是最匹配的）
	movieResult := result.Results[0]

	// 填充媒体信息
	mediaInfo.TMDBID = movieResult.ID
	mediaInfo.Overview = movieResult.Overview
	mediaInfo.PosterPath = movieResult.PosterPath
	mediaInfo.BackdropPath = movieResult.BackdropPath
	mediaInfo.VoteAverage = movieResult.VoteAverage
	mediaInfo.VoteCount = movieResult.VoteCount
	mediaInfo.ReleaseDate = movieResult.ReleaseDate
	mediaInfo.Genres = movieResult.GenreIDs
	mediaInfo.Popularity = movieResult.Popularity
	mediaInfo.OriginalTitle = movieResult.OriginalTitle

	// 可选：获取更详细的电影信息
	if details, err := tmdbClient.GetMovieDetails(movieResult.ID, "zh-CN"); err == nil {
		mediaInfo.MovieResult = details
		// 从生产国家中提取国家代码
		if len(details.ProductionCountries) > 0 {
			var countries []string
			for _, country := range details.ProductionCountries {
				if country.ISO3166_1 != "" {
					countries = append(countries, country.ISO3166_1)
				}
			}
			mediaInfo.OriginCountry = countries
		}
	}

	// 设置拼音首字母
	mediaInfo.PinyinInitials = GetPinyinInitials(mediaInfo.OriginalTitle)
	if mediaInfo.PinyinInitials == "" {
		// 如果OriginalTitle为空，使用Title
		mediaInfo.PinyinInitials = GetPinyinInitials(mediaInfo.Title)
	}

	return nil
}

// getRegionByCountryCode 根据国家代码获取地区分类
func (ps *PendingScanner) getRegionByCountryCode(countryCode string) string {
	// 华语
	chineseCountries := map[string]bool{
		"CN": true, // 中国
		"HK": true, // 中国香港
		"MO": true, // 中国澳门
		"TW": true, // 中国台湾
	}

	// 北美
	northAmericanCountries := map[string]bool{
		"US": true, // 美国
		"CA": true, // 加拿大
		"MX": true, // 墨西哥
	}

	// 欧洲
	europeanCountries := map[string]bool{
		"GB": true, // 英国
		"FR": true, // 法国
		"IT": true, // 意大利
		"DE": true, // 德国
		"ES": true, // 西班牙
		"SE": true, // 瑞典
		"DK": true, // 丹麦
		"NO": true, // 挪威
		"FI": true, // 芬兰
		"RU": true, // 俄罗斯
		"PL": true, // 波兰
		"CZ": true, // 捷克
		"HU": true, // 匈牙利
		"NL": true, // 荷兰
		"BE": true, // 比利时
		"CH": true, // 瑞士
		"AT": true, // 奥地利
		"GR": true, // 希腊
		"PT": true, // 葡萄牙
		"IE": true, // 爱尔兰
	}

	// 日本
	if countryCode == "JP" {
		return "日本"
	}

	// 韩国
	if countryCode == "KR" {
		return "韩国"
	}

	// 印度
	if countryCode == "IN" {
		return "印度"
	}

	// 东南亚
	southeastAsianCountries := map[string]bool{
		"TH": true, // 泰国
		"ID": true, // 印度尼西亚
		"PH": true, // 菲律宾
		"VN": true, // 越南
		"MY": true, // 马来西亚
		"SG": true, // 新加坡
		"MM": true, // 缅甸
		"LA": true, // 老挝
		"KH": true, // 柬埔寨
	}

	// 大洋洲
	oceanianCountries := map[string]bool{
		"AU": true, // 澳大利亚
		"NZ": true, // 新西兰
	}

	// 拉丁美洲
	latinAmericanCountries := map[string]bool{
		"BR": true, // 巴西
		"AR": true, // 阿根廷
		"CL": true, // 智利
	}

	// 中东/西亚
	middleEasternCountries := map[string]bool{
		"IR": true, // 伊朗
		"IL": true, // 以色列
		"TR": true, // 土耳其
		"LB": true, // 黎巴嫩
	}

	// 非洲
	africanCountries := map[string]bool{
		"EG": true, // 埃及
		"ZA": true, // 南非
		"NG": true, // 尼日利亚
		"SN": true, // 塞内加尔
	}

	// 检查地区分类
	if chineseCountries[countryCode] {
		return "华语"
	}
	if northAmericanCountries[countryCode] {
		return "北美"
	}
	if europeanCountries[countryCode] {
		return "欧洲"
	}
	if southeastAsianCountries[countryCode] {
		return "东南亚"
	}
	if oceanianCountries[countryCode] {
		return "大洋洲"
	}
	if latinAmericanCountries[countryCode] {
		return "拉丁美洲"
	}
	if middleEasternCountries[countryCode] {
		return "中东西亚"
	}
	if africanCountries[countryCode] {
		return "非洲"
	}

	// 其他地区
	return "其他地区"
}

// getTargetRegion 根据媒体信息获取目标地区
func (ps *PendingScanner) getTargetRegion(mediaInfo *MediaInfo) string {
	// 如果没有国家信息，返回空字符串
	if len(mediaInfo.OriginCountry) == 0 {
		return ""
	}

	// 取第一个国家代码作为主要国家
	primaryCountry := mediaInfo.OriginCountry[0]
	return ps.getRegionByCountryCode(primaryCountry)
}

// cleanFolderName 清理文件夹名称中的非法字符
func cleanFolderName(name string) string {
	// 移除或替换Windows文件名中的非法字符
	illegalChars := []string{"\\", "/", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range illegalChars {
		name = strings.ReplaceAll(name, char, "_")
	}

	// 移除多余的空格和连字符
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".-_ ")

	// 限制长度（Windows路径最大260字符，但我们需要留有余地）
	if len(name) > 200 {
		name = name[:200]
	}

	return name
}

// createFolderInSelectedCID 在目标目录中创建文件夹（支持多层路径）
func (ps *PendingScanner) createFolderInSelectedCID(client *driver.Pan115Client, parentCID, folderPath string) (string, error) {
	// 如果路径包含斜杠，需要逐层创建目录
	if strings.Contains(folderPath, "/") {
		return ps.createFolderRecursive(client, parentCID, folderPath)
	}

	// 单层目录创建逻辑
	// 首先检查文件夹是否已存在
	folders, err := ps.app.rateLimitedList(client, parentCID)
	if err != nil {
		return "", fmt.Errorf("获取目录列表失败: %v", err)
	}

	// 查找是否已存在同名文件夹
	for _, folder := range *folders {
		if folder.IsDirectory && folder.Name == folderPath {
			log.Printf("文件夹已存在: %s (CID: %s)", folderPath, folder.FileID)
			return folder.FileID, nil
		}
	}

	// 创建新文件夹
	log.Printf("创建新文件夹: %s 在目录: %s", folderPath, parentCID)

	// 使用115网盘的创建文件夹API
	newFolderCID, err := ps.app.rateLimitedMkdir(client, parentCID, folderPath)
	if err != nil {
		return "", fmt.Errorf("创建文件夹失败: %v", err)
	}

	log.Printf("文件夹创建成功: %s (CID: %s)", folderPath, newFolderCID)
	return newFolderCID, nil
}

// createFolderRecursive 递归创建多层目录
func (ps *PendingScanner) createFolderRecursive(client *driver.Pan115Client, parentCID, folderPath string) (string, error) {
	// 分割路径为各级目录
	parts := strings.Split(folderPath, "/")
	currentCID := parentCID
	currentPath := ""

	stat, err := ps.app.rateLimitedStat(client, parentCID)
	if err == nil {
		for i, parent := range stat.Parents {
			log.Printf("parent[%d] ID[%s] name[%s]", i, parent.ID, parent.Name)
			if parent.ID != "0" {
				currentPath = currentPath + "/" + parent.Name
			}
		}
		currentPath = currentPath + "/" + stat.Name
		log.Printf("当前目录CID[%s], 路径[%s]", currentCID, currentPath)

		currentPath = currentPath + "/" + folderPath
		dirInfo, err := ps.app.rateLimitedDirName2CID(client, currentPath)
		if err == nil && dirInfo.CategoryID != "0" {
			log.Printf("获取[%s]目录ID[%s]", currentPath, dirInfo.CategoryID)
			return string(dirInfo.CategoryID), nil
		} else {
			log.Printf("获取[%s]目录ID失败: %v", currentPath, err)
		}
	}

	// 逐层创建目录
	for i, part := range parts {
		if part == "" {
			continue // 跳过空的部分
		}

		// 检查当前目录下是否已存在该文件夹
		folders, err := ps.app.rateLimitedList(client, currentCID)
		if err != nil {
			return "", fmt.Errorf("获取目录列表失败: %v", err)
		}

		var foundCID string
		for _, folder := range *folders {
			if folder.IsDirectory && folder.Name == part {
				foundCID = folder.FileID
				log.Printf("文件夹已存在: %s (CID: %s)", part, foundCID)
				break
			}
		}

		// 如果不存在，创建新文件夹
		if foundCID == "" {
			log.Printf("创建文件夹: %s 在目录: %s", part, currentCID)
			newCID, err := ps.app.rateLimitedMkdir(client, currentCID, part)
			if err != nil {
				return "", fmt.Errorf("创建文件夹失败: %s, 错误: %v", part, err)
			}
			foundCID = newCID
			log.Printf("文件夹创建成功: %s (CID: %s)", part, foundCID)
		}

		// 更新当前CID为下一层的父目录
		currentCID = foundCID

		// 如果是最后一层，返回最终的CID
		if i == len(parts)-1 {
			return currentCID, nil
		}
	}

	return currentCID, nil
}

// findExistingFolder 在existingCID目录中查找是否存在相同的文件夹
func (ps *PendingScanner) findExistingFolder(client *driver.Pan115Client, existingCID, targetFolderPath, rawFolderName string) (string, bool) {
	if existingCID == "" {
		return "", false
	}

	// 首先尝试按完整路径查找
	fullPath := targetFolderPath
	if !strings.HasSuffix(fullPath, "/"+rawFolderName) {
		fullPath = fullPath + "/" + rawFolderName
	}

	// 递归查找文件夹
	cid, found := ps.findFolderByPath(client, existingCID, fullPath)
	if found {
		return cid, true
	}

	// 如果按完整路径找不到，尝试只按原始文件夹名查找（可能在不同的路径下）
	// 这需要遍历整个existingCID目录，可能会比较慢
	log.Printf("按完整路径未找到文件夹，开始搜索原始文件夹名: %s", rawFolderName)

	// 获取existingCID目录下的所有文件夹
	folders, err := ps.app.rateLimitedList(client, existingCID)
	if err != nil {
		log.Printf("获取existingCID目录列表失败: %v", err)
		return "", false
	}

	// 递归搜索所有子目录
	return ps.searchFolderRecursive(client, folders, rawFolderName)
}

// findFolderByPath 按路径查找文件夹
func (ps *PendingScanner) findFolderByPath(client *driver.Pan115Client, parentCID, folderPath string) (string, bool) {
	// 分割路径
	parts := strings.Split(folderPath, "/")
	currentCID := parentCID

	for i, part := range parts {
		if part == "" {
			continue
		}

		// 获取当前目录下的文件夹列表
		folders, err := ps.app.rateLimitedList(client, currentCID)
		if err != nil {
			log.Printf("获取目录列表失败: %s, 错误: %v", currentCID, err)
			return "", false
		}

		// 查找匹配的文件夹
		var foundCID string
		for _, folder := range *folders {
			if folder.IsDirectory && folder.Name == part {
				foundCID = folder.FileID
				break
			}
		}

		if foundCID == "" {
			// 当前层没找到
			return "", false
		}

		currentCID = foundCID

		// 如果是最后一层，返回找到的CID
		if i == len(parts)-1 {
			return currentCID, true
		}
	}

	return "", false
}

// searchFolderRecursive 递归搜索文件夹
func (ps *PendingScanner) searchFolderRecursive(client *driver.Pan115Client, folders *[]driver.File, targetName string) (string, bool) {
	for _, folder := range *folders {
		if !folder.IsDirectory {
			continue
		}

		// 检查当前文件夹是否匹配
		if folder.Name == targetName {
			return folder.FileID, true
		}

		// 递归搜索子目录
		subFolders, err := ps.app.rateLimitedList(client, folder.FileID)
		if err != nil {
			log.Printf("获取子目录列表失败: %s, 错误: %v", folder.FileID, err)
			continue
		}

		if cid, found := ps.searchFolderRecursive(client, subFolders, targetName); found {
			return cid, true
		}
	}

	return "", false
}

// moveMissingFiles 移动源文件夹中缺少的文件到目标文件夹
func (ps *PendingScanner) moveMissingFiles(client *driver.Pan115Client, sourceFolderCID, targetFolderCID string) error {
	// 获取源文件夹中的所有文件
	sourceFiles, err := ps.app.rateLimitedList(client, sourceFolderCID)
	if err != nil {
		return fmt.Errorf("获取源文件夹文件列表失败: %v", err)
	}

	// 获取目标文件夹中的所有文件
	targetFiles, err := ps.app.rateLimitedList(client, targetFolderCID)
	if err != nil {
		return fmt.Errorf("获取目标文件夹文件列表失败: %v", err)
	}

	// 创建目标文件名的映射
	targetFileMap := make(map[string]driver.File)
	for _, file := range *targetFiles {
		targetFileMap[file.Name] = file
	}

	// 找出源文件夹中缺少的文件（在目标文件夹中不存在的文件）
	var missingFiles []driver.File
	for _, file := range *sourceFiles {
		if _, exists := targetFileMap[file.Name]; !exists {
			missingFiles = append(missingFiles, file)
		} else {
			log.Printf("文件已存在，跳过: %s", file.Name)
		}
	}

	if len(missingFiles) == 0 {
		log.Printf("没有缺少的文件，所有文件都已存在")
		return nil
	}

	log.Printf("发现 %d 个缺少的文件需要移动", len(missingFiles))

	// 移动缺少的文件
	for _, file := range missingFiles {
		log.Printf("移动文件: %s", file.Name)
		if err := ps.app.rateLimitedMove(client, targetFolderCID, file.FileID); err != nil {
			return fmt.Errorf("移动文件失败: %s, 错误: %v", file.Name, err)
		}
		log.Printf("文件移动成功: %s", file.Name)
	}

	log.Printf("成功移动 %d 个缺少的文件", len(missingFiles))
	return nil
}

// GetPendingScanStatus 获取待整理目录扫描状态
func (ps *PendingScanner) GetPendingScanStatus() map[string]interface{} {
	ps.app.mu.Lock()
	defer ps.app.mu.Unlock()

	return map[string]interface{}{
		"pendingCID":   ps.app.cfg.PendingCID,
		"existingCID":  ps.app.cfg.ExistingCID,
		"redundantCID": ps.app.cfg.RedundantCID,
		"lastScanAt":   ps.app.lastScanAt,
		"lastScanMsg":  ps.app.lastScanMsg,
		"lastErr":      ps.app.lastErr,
	}
}
