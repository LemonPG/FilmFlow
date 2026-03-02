package test

import (
	"fmt"
	"testing"

	"filmflow/internal/app"
)

func TestParseFolderName(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected app.MediaInfo
	}{

		{
			name:  "电视剧示例1",
			input: "Taxi Driver S03 2025 HDTV 1080i MPEG2 AC3-UltraTV",
			expected: app.MediaInfo{
				Title:    "Taxi Driver",
				Season:   "S03",
				Year:     "2025",
				IsTVShow: true,
			},
		},
		{
			name:  "电影示例1",
			input: "New.Shaolin.Boxers.1976.1080p.BluRay.x265.10bit.FLAC.2.0-ADE",
			expected: app.MediaInfo{
				Title:    "New Shaolin Boxers",
				Season:   "",
				Year:     "1976",
				IsTVShow: false,
			},
		},
		{
			name:  "电影示例2",
			input: "Crack.Cocaine.Corruption.&.Conspiracy.2021.1080p.NF.WEB-DL.DDP5.1.H264-HHWEB",
			expected: app.MediaInfo{
				Title:    "Crack Cocaine Corruption & Conspiracy",
				Season:   "",
				Year:     "2021",
				IsTVShow: false,
			},
		},
		{
			name:  "电视剧示例2",
			input: "Rite.of.Passage.S01.2025.2160p.WEB-DL.H264.AAC-ADWeb",
			expected: app.MediaInfo{
				Title:    "Rite of Passage",
				Season:   "S01",
				Year:     "2025",
				IsTVShow: true,
			},
		},
		{
			name:  "电视剧示例3",
			input: "Rascal.Does.Not.Dream.of.Bunny.Girl.Senpai.S02.2025.1080p.CR.WEB-DL.x264.AAC-AnimeF@ADWeb",
			expected: app.MediaInfo{
				Title:    "Rascal Does Not Dream of Bunny Girl Senpai",
				Season:   "S02",
				Year:     "2025",
				IsTVShow: true,
			},
		},
		{
			name:  "电视剧示例4",
			input: "One-Punch.Man.S03.2025.1080p.CR.WEB-DL.x264.AAC-Nest@ADWeb",
			expected: app.MediaInfo{
				Title:    "One Punch Man",
				Season:   "S03",
				Year:     "2025",
				IsTVShow: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := app.ParseFolderName(tc.input)
			if err != nil {
				t.Errorf("解析失败: %v", err)
				return
			}

			// 检查标题
			if result.Title != tc.expected.Title {
				t.Errorf("标题不匹配: 期望 %s, 实际 %s", tc.expected.Title, result.Title)
			}

			// 检查季
			if result.Season != tc.expected.Season {
				t.Errorf("季不匹配: 期望 %s, 实际 %s", tc.expected.Season, result.Season)
			}

			// 检查年份
			if result.Year != tc.expected.Year {
				t.Errorf("年份不匹配: 期望 %s, 实际 %s", tc.expected.Year, result.Year)
			}

			// 检查是否电视剧
			if result.IsTVShow != tc.expected.IsTVShow {
				t.Errorf("是否电视剧不匹配: 期望 %v, 实际 %v", tc.expected.IsTVShow, result.IsTVShow)
			}

			fmt.Printf("✓ %s: %s -> %s\n", tc.name, tc.input, result.String())
		})
	}
}

func TestCleanTitle(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "清理质量标识符",
			input:    "Movie Name 1080p BluRay x264 AC3",
			expected: "Movie Name",
		},
		{
			name:     "清理发布组信息",
			input:    "Movie Name 2023 WEB-DL x264-ReleaseGroup",
			expected: "Movie Name",
		},
		{
			name:     "清理多个质量标识符",
			input:    "TV Show S01 2024 2160p UHD BluRay x265 DDP5.1",
			expected: "TV Show",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 由于cleanTitle是私有函数，我们通过ParseFolderName间接测试
			result, err := app.ParseFolderName(tc.input)
			if err != nil {
				t.Errorf("解析失败: %v", err)
				return
			}

			// 检查清理后的标题
			if result.Title != tc.expected {
				t.Errorf("清理标题不匹配: 期望 %s, 实际 %s", tc.expected, result.Title)
			}
		})
	}
}
