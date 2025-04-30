package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 定义正则表达式模式常量，避免重复编译
const (
	aigPattern = `AIG:(\s*([0-9.]+))`
	fixPattern = `^[0-9a-f]{40} '[^']+' \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} (fix)`
	// 添加提交信息解析模式
	commitPattern = `^([0-9a-f]{40}) '([^']+)' (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) (.+)$`
	// 添加文件扩展名常量
	includeFileExts = ".html,.vue,.js,.ts,.tsx,.css,.scss,.cjs,.go,.php,.yaml,.proto"
	excludeFileExts = ".pb.go,.pb.validate.go"
)

type CommitStats struct {
	AddedLines   int
	DeletedLines int
	AIGRatio     float64
	IsFix        bool
}

// AI代码统计脚本
func main() {
	author, since, until, err := parseCommandLineArgs()
	if err != nil {
		fmt.Println(err)
		return
	}

	output, err := runGitCommand(author, since, until)
	if err != nil {
		fmt.Println(err)
		return
	}

	commits := splitCommits(output)
	stats := analyzeCommits(commits)
	printStatistics(author, since, until, stats)
}

// 解析命令行参数
func parseCommandLineArgs() (string, string, string, error) {
	var author, since, until string
	if len(os.Args) > 1 {
		author = os.Args[1]
	}
	if len(os.Args) > 2 {
		since = os.Args[2]
		if _, err := time.Parse("2006-01-02", since); err != nil {
			return "", "", "", fmt.Errorf("错误：起始日期 '%s' 格式不正确，请使用 '2006-01-02' 格式", since)
		}
	}
	if len(os.Args) > 3 {
		until = os.Args[3]
		if _, err := time.Parse("2006-01-02", until); err != nil {
			return "", "", "", fmt.Errorf("错误：结束日期 '%s' 格式不正确，请使用 '2006-01-02' 格式", until)
		}
	}

	since, until = getDefaultDateRange(since, until)
	return author, since, until, nil
}

// 获取默认日期范围
func getDefaultDateRange(since, until string) (string, string) {
	if since != "" && until != "" {
		return since, until
	}

	now := time.Now()
	year, month, day := now.Date()
	location := now.Location()

	var periodStart, periodEnd time.Time

	if day <= 15 {
		// 当前在上半月，则统计上月16号到月底的数据
		firstOfThisMonth := time.Date(year, month, 1, 0, 0, 0, 0, location)
		lastMonth := firstOfThisMonth.AddDate(0, -1, 0)
		lastMonthYear, lastMonthMonth, _ := lastMonth.Date()

		periodStart = time.Date(lastMonthYear, lastMonthMonth, 16, 0, 0, 0, 0, location)
		firstOfNextMonth := time.Date(lastMonthYear, lastMonthMonth+1, 1, 0, 0, 0, 0, location)
		periodEnd = firstOfNextMonth.AddDate(0, 0, -1)
	} else {
		// 当前在下半月，则统计本月1号到15号的数据
		periodStart = time.Date(year, month, 1, 0, 0, 0, 0, location)
		periodEnd = time.Date(year, month, 15, 0, 0, 0, 0, location)
	}

	return periodStart.Format("2006-01-02"), periodEnd.Format("2006-01-02")
}

// 运行 Git 命令
func runGitCommand(author, since, until string) (string, error) {
	cmdArgs := []string{
		"log",
		"--all",
		"--since=" + since,
		"--until=" + until,
		"--pretty=format:%H '%an' %ad %s %b",
		"--numstat",
		"--date=format:%Y-%m-%d %H:%M:%S",
		"--no-merges",
	}

	if author != "" {
		cmdArgs = append(cmdArgs, "--author="+author)
	}

	cmd := exec.Command("git", cmdArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("执行 git 命令时出错: %v", err)
	}
	return out.String(), nil
}

// 分析提交信息
func analyzeCommits(commits []string) map[string]int {
	stats := make(map[string]int)
	aigRegex := regexp.MustCompile(aigPattern)
	fixRegex := regexp.MustCompile(fixPattern)

	includeExts := strings.Split(includeFileExts, ",")
	excludeExts := strings.Split(excludeFileExts, ",")

	for _, commit := range commits {
		if commit == "" {
			continue
		}

		commitStats := processCommit(commit, aigRegex, fixRegex, includeExts, excludeExts)
		updateStats(stats, commitStats)
	}

	return stats
}

// 处理单个提交
func processCommit(commit string, aigRegex, fixRegex *regexp.Regexp, includeExts, excludeExts []string) CommitStats {
	lines := strings.Split(commit, "\n")
	if len(lines) == 0 {
		return CommitStats{}
	}

	// 获取提交的第一行作为基本信息
	firstLine := lines[0]

	// 解析提交的基本信息（ID、作者、时间）
	commitRegex := regexp.MustCompile(commitPattern)
	matches := commitRegex.FindStringSubmatch(firstLine)
	if len(matches) < 5 {
		return CommitStats{}
	}

	commitID := matches[1]
	author := matches[2]
	commitTime := matches[3]

	// 查找文件变更列表的起始位置
	fileChangeStartIdx := 1
	var messageLines []string
	messageLines = append(messageLines, matches[4])

	// 遍历每一行，直到找到文件变更记录
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}

		// 检查是否是文件变更记录（通过判断行的格式）
		if isFileChangeLine(line) {
			fileChangeStartIdx = i
			break
		}
		messageLines = append(messageLines, line)
	}

	// 合并提交消息
	message := strings.Join(messageLines, "\n")

	// 获取文件变更列表
	fileChanges := lines[fileChangeStartIdx:]

	// 打印提交信息
	fmt.Printf("\n提交详情:\n")
	fmt.Printf("  提交ID: %s\n", commitID)
	fmt.Printf("  作者: %s\n", author)
	fmt.Printf("  时间: %s\n", commitTime)
	fmt.Printf("  消息:\n")
	// 打印多行消息，每行前面加缩进
	for _, line := range strings.Split(message, "\n") {
		if strings.TrimSpace(line) != "" {
			fmt.Printf("    %s\n", line)
		}
	}

	stats := CommitStats{
		AIGRatio: extractAIGRatio(aigRegex, message),
		IsFix:    fixRegex.MatchString(firstLine),
	}

	fmt.Printf("  AI贡献率: %.2f%%\n", stats.AIGRatio*100)
	fmt.Printf("  是否修复提交: %v\n", stats.IsFix)
	fmt.Printf("  变更文件:\n")

	for _, change := range fileChanges {
		if change == "" {
			continue
		}

		added, deleted, fileName := parseFileChange(change)
		if !isValidFile(fileName, includeExts, excludeExts) {
			fmt.Printf("    [跳过] %s (不符合统计条件)\n", fileName)
			continue
		}

		fmt.Printf("    - %s (添加: %d, 删除: %d)\n", fileName, added, deleted)
		stats.AddedLines += added
		stats.DeletedLines += deleted
	}

	aiAddedLines := int(math.Round(float64(stats.AddedLines) * stats.AIGRatio))
	aiDeletedLines := int(math.Round(float64(stats.DeletedLines) * stats.AIGRatio))
	fmt.Printf("  本次提交总计:\n")
	fmt.Printf("    总添加行数: %d\n", stats.AddedLines)
	fmt.Printf("    总删除行数: %d\n", stats.DeletedLines)
	fmt.Printf("    AI贡献添加行数: %d\n", aiAddedLines)
	fmt.Printf("    AI贡献删除行数: %d\n", aiDeletedLines)
	fmt.Printf("  %s\n", strings.Repeat("-", 80))

	return stats
}

// 判断是否为文件变更记录行
func isFileChangeLine(line string) bool {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return false
	}

	// 检查前两个字段是否都是数字或 "-"
	for _, field := range parts[:2] {
		if field != "-" {
			_, err := strconv.Atoi(field)
			if err != nil {
				return false
			}
		}
	}
	return true
}

// 解析文件变更信息
func parseFileChange(change string) (added, deleted int, fileName string) {
	parts := strings.Fields(change)
	if len(parts) < 3 {
		return 0, 0, ""
	}

	added, _ = strconv.Atoi(parts[0])
	deleted, _ = strconv.Atoi(parts[1])
	if len(parts) > 3 {
		fileName = strings.Join(parts[2:], "")
	} else {
		fileName = parts[2]
	}

	return added, deleted, fileName
}

// 检查文件是否应该被统计
func isValidFile(fileName string, includeExts, excludeExts []string) bool {
	ext := filepath.Ext(fileName)
	for _, excludeExt := range excludeExts {
		if ext == excludeExt {
			return false
		}
	}
	for _, includeExt := range includeExts {
		if ext == includeExt {
			return true
		}
	}
	return false
}

// 更新统计信息
func updateStats(stats map[string]int, commitStats CommitStats) {
	stats["totalAddedLines"] += commitStats.AddedLines
	stats["totalDeletedLines"] += commitStats.DeletedLines

	aiAddedLines := int(math.Round(float64(commitStats.AddedLines) * commitStats.AIGRatio))
	aiDeletedLines := int(math.Round(float64(commitStats.DeletedLines) * commitStats.AIGRatio))

	stats["totalAIAddedLines"] += aiAddedLines
	stats["totalAIDeletedLines"] += aiDeletedLines

	if commitStats.IsFix {
		stats["fixCount"]++
		if commitStats.AIGRatio > 0 {
			stats["fixAndAIGCount"]++
		}
	}
}

// 提取 AIG 比例
func extractAIGRatio(re *regexp.Regexp, commit string) float64 {
	matches := re.FindStringSubmatch(commit)
	if len(matches) > 2 {
		ratio, err := strconv.ParseFloat(matches[2], 64)
		if err != nil || ratio < 0 {
			return 0
		}
		return ratio
	}
	return 0
}

// 分割提交信息
func splitCommits(output string) []string {
	var commits []string
	lines := strings.Split(output, "\n")
	var currentCommit strings.Builder

	for _, line := range lines {
		if line == "" {
			continue
		}
		s := strings.Fields(line)
		if len(s) > 0 && len(s[0]) == 40 && currentCommit.Len() > 0 {
			commits = append(commits, currentCommit.String())
			currentCommit.Reset()
		}
		if currentCommit.Len() > 0 {
			currentCommit.WriteByte('\n')
		}
		currentCommit.WriteString(line)
	}

	if currentCommit.Len() > 0 {
		commits = append(commits, currentCommit.String())
	}
	return commits
}

// 打印统计结果
func printStatistics(author, since, until string, stats map[string]int) {
	// 计算占比
	var addedRatio, deletedRatio, aiBugContribution float64

	if stats["totalAddedLines"] > 0 {
		addedRatio = float64(stats["totalAIAddedLines"]) / float64(stats["totalAddedLines"]) * 100
	}
	if stats["totalDeletedLines"] > 0 {
		deletedRatio = float64(stats["totalAIDeletedLines"]) / float64(stats["totalDeletedLines"]) * 100
	}
	if stats["fixCount"] > 0 {
		aiBugContribution = float64(stats["fixAndAIGCount"]) / float64(stats["fixCount"]) * 100
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 80))
	fmt.Printf("统计结果汇总:\n")
	fmt.Printf("%s\n", strings.Repeat("-", 80))
	fmt.Printf("  分析范围:\n")
	fmt.Printf("    作者: %s\n", author)
	fmt.Printf("    开始时间: %s\n", since)
	fmt.Printf("    结束时间: %s\n", until)
	fmt.Printf("\n  代码变更统计:\n")
	fmt.Printf("    总代码添加: %d 行\n", stats["totalAddedLines"])
	fmt.Printf("    总代码删除: %d 行\n", stats["totalDeletedLines"])
	fmt.Printf("    AI贡献添加: %d 行 (%.2f%%)\n", stats["totalAIAddedLines"], addedRatio)
	fmt.Printf("    AI贡献删除: %d 行 (%.2f%%)\n", stats["totalAIDeletedLines"], deletedRatio)
	fmt.Printf("\n  Bug修复统计:\n")
	fmt.Printf("    总修复提交: %d 次\n", stats["fixCount"])
	fmt.Printf("    AI参与修复: %d 次\n", stats["fixAndAIGCount"])
	fmt.Printf("    AI修复贡献率: %.2f%%\n", aiBugContribution)
	fmt.Printf("%s\n", strings.Repeat("=", 80))
}
