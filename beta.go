//go:build beta
// +build beta

package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// 全局调试标志
var debug bool

// PhoneDatabase 手机号数据库
type PhoneDatabase struct {
	records  map[string]*PhoneInfo // key: 手机号前7位
	prefixes []string              // 排序后的前缀列表，用于二分查找
	sorted   bool                  // 是否已排序
}

// PhoneInfo 查询结果
type PhoneInfo struct {
	Province string
	City     string
	ZipCode  string
	AreaCode string
	CardType string
}

// 对象池
var phoneInfoPool = sync.Pool{
	New: func() interface{} { return &PhoneInfo{} },
}

func main() {
	// 解析命令行参数
	debugFlag := flag.Bool("debug", false, "输出调试信息（状态和错误信息）")
	flag.Parse()
	debug = *debugFlag

	// 配置参数
	inputFile := "phones.txt"
	outputFile := "result.txt"
	phoneDataFile := "phone2region.txt"

	// 加载数据库
	fmt.Println("🔧 正在加载手机号数据库...")
	db, err := LoadPhoneDatabase(phoneDataFile)
	if err != nil {
		fmt.Printf("❌ 加载手机号数据库失败: %v\n", err)
		fmt.Println("请确保 phone2region.txt 文件存在，可以从以下地址下载：")
		fmt.Println("https://github.com/ALI1416/phone2region/blob/master/data/phone2region.txt")
		return
	}
	fmt.Printf("✅ 成功加载手机号数据库，共 %d 条号段记录\n", len(db.records))

	// 流式处理手机号文件
	err = processPhonesStreaming(inputFile, outputFile, db)
	if err != nil {
		fmt.Printf("❌ 处理失败: %v\n", err)
		return
	}
}

// LoadPhoneDatabase 加载 phone2region.txt 文件
func LoadPhoneDatabase(filename string) (*PhoneDatabase, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	db := &PhoneDatabase{
		records:  make(map[string]*PhoneInfo),
		prefixes: make([]string, 0),
		sorted:   false,
	}

	scanner := bufio.NewScanner(file)
	// 设置更大的缓冲区（支持大文件）
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	successCount := 0
	errorCount := 0
	skipCount := 0

	fmt.Println("📖 正在解析数据库文件...")

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 按 | 分隔符解析
		parts := strings.Split(line, "|")
		if len(parts) < 6 {
			errorCount++
			if errorCount <= 5 && debug {
				fmt.Printf("   ⚠️ 第%d行格式错误(字段数不足): %s\n", lineNum, line)
			}
			continue
		}

		// 清理每个字段的空格
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}

		// 提取手机号段（前7位）
		phonePrefix := parts[0]

		// 验证号段
		if len(phonePrefix) != 7 {
			if len(phonePrefix) > 7 {
				phonePrefix = phonePrefix[:7]
			} else if len(phonePrefix) < 7 {
				skipCount++
				continue
			}
		}

		// 验证是否为数字
		if !isNumeric(phonePrefix) {
			skipCount++
			continue
		}

		// 解析字段
		province := parts[1]
		city := parts[2]
		zipCode := parts[3]
		areaCode := parts[4]
		cardType := parts[5]

		// 处理空值
		if zipCode == "0" || zipCode == "" {
			zipCode = ""
		}
		if areaCode == "0" || areaCode == "" {
			areaCode = ""
		}
		if cardType == "" {
			cardType = "未知"
		}

		// 运营商名称标准化
		cardType = normalizeCardType(cardType)

		// 存储到map
		db.records[phonePrefix] = &PhoneInfo{
			Province: province,
			City:     city,
			ZipCode:  zipCode,
			AreaCode: areaCode,
			CardType: cardType,
		}
		db.prefixes = append(db.prefixes, phonePrefix)
		successCount++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	if successCount == 0 {
		return nil, fmt.Errorf("没有解析到任何有效数据，共处理 %d 行，%d 个错误，%d 行跳过", lineNum, errorCount, skipCount)
	}

	// 排序前缀列表，用于二分查找
	sort.Strings(db.prefixes)
	db.sorted = true

	fmt.Printf("   ✅ 成功解析 %d 条号段记录，跳过 %d 行错误，%d 行无效数据\n", successCount, errorCount, skipCount)
	return db, nil
}

// normalizeCardType 标准化运营商名称
func normalizeCardType(cardType string) string {
	cardTypeLower := strings.ToLower(cardType)
	switch {
	case strings.Contains(cardTypeLower, "移动"):
		return "中国移动"
	case strings.Contains(cardTypeLower, "联通"):
		return "中国联通"
	case strings.Contains(cardTypeLower, "电信"):
		return "中国电信"
	case strings.Contains(cardTypeLower, "广电"):
		return "中国广电"
	default:
		return cardType
	}
}

// Query 使用二分查找查询手机号归属地
func (db *PhoneDatabase) Query(phonePrefix string) *PhoneInfo {
	if !db.sorted {
		// 如果未排序，先排序
		sort.Strings(db.prefixes)
		db.sorted = true
	}

	// 二分查找
	idx := sort.SearchStrings(db.prefixes, phonePrefix)

	// 精确匹配
	if idx < len(db.prefixes) && db.prefixes[idx] == phonePrefix {
		if info, ok := db.records[phonePrefix]; ok {
			return info
		}
	}

	// 如果没找到，尝试模糊匹配（前缀匹配）
	if len(phonePrefix) == 7 {
		// 尝试前6位、前5位等
		for i := 6; i >= 3; i-- {
			shortPrefix := phonePrefix[:i]
			searchIdx := sort.SearchStrings(db.prefixes, shortPrefix)
			if searchIdx < len(db.prefixes) && strings.HasPrefix(db.prefixes[searchIdx], shortPrefix) {
				if info, ok := db.records[db.prefixes[searchIdx]]; ok {
					if debug {
						fmt.Printf("\n[DEBUG] 模糊匹配: %s -> %s\n", phonePrefix, db.prefixes[searchIdx])
					}
					return info
				}
			}
		}
	}

	return nil
}

// isHeaderLine 判断文本行是否为表头
func isHeaderLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "mobile") || strings.Contains(lower, "手机号") || strings.Contains(lower, "cardno") || strings.Contains(lower, "username") || strings.Contains(lower, "address")
}

// isDigitByte 判断字节是否为数字字符
func isDigitByte(b byte) bool {
	return b >= '0' && b <= '9'
}

// extractPhonesFromBytes 从字节切片中提取所有手机号（零分配，按字节扫描）
func extractPhonesFromBytes(line []byte) []string {
	lineLen := len(line)
	if lineLen < 11 {
		return nil
	}

	var phones []string

	for i := 0; i <= lineLen-11; i++ {
		// 检查前面不能是数字（负向后顾）
		if i > 0 && isDigitByte(line[i-1]) {
			continue
		}

		// 检查第一位必须是 '1'
		if line[i] != '1' {
			continue
		}

		// 检查第二位必须在 3-9 之间
		second := line[i+1]
		if second < '3' || second > '9' {
			continue
		}

		// 检查第3-11位必须都是数字（共9位）
		isValid := true
		for j := 2; j <= 10; j++ {
			if !isDigitByte(line[i+j]) {
				isValid = false
				break
			}
		}
		if !isValid {
			continue
		}

		// 检查后面不能是数字（正向后顾）
		if i+11 < lineLen && isDigitByte(line[i+11]) {
			continue
		}

		// 找到有效手机号
		phone := string(line[i : i+11])
		phones = append(phones, phone)

		if debug {
			fmt.Printf("DEBUG: 提取到手机号: %s\n", phone)
		}

		// 跳过已检查的部分，避免重复匹配
		i += 10
	}

	return phones
}

// ResetPhoneInfo 重置 PhoneInfo 以便放回池中复用
func ResetPhoneInfo(info *PhoneInfo) {
	info.Province = ""
	info.City = ""
	info.ZipCode = ""
	info.AreaCode = ""
	info.CardType = ""
}

// isUnknown 判断是否为未知归属地
func isUnknown(info *PhoneInfo) bool {
	return info.Province == "未知" || info.City == "未知" || info.Province == ""
}

// 检查字符串是否全是数字
func isNumeric(s string) bool {
	for _, char := range s {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// processPhonesStreaming 流式处理手机号文件
func processPhonesStreaming(inputFile, outputFile string, db *PhoneDatabase) error {
	input, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("打开输入文件失败: %w", err)
	}
	defer input.Close()

	output, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer output.Close()

	// 写入 UTF-8 BOM 头
	if _, err := output.Write([]byte("\uFEFF")); err != nil {
		return fmt.Errorf("写入UTF-8 BOM失败: %w", err)
	}

	bufOut := bufio.NewWriterSize(output, 1<<20) // 1MB 缓冲区
	csvWriter := csv.NewWriter(bufOut)
	defer func() {
		csvWriter.Flush()
		bufOut.Flush()
	}()

	// 写入 CSV 头部
	if err := csvWriter.Write([]string{"手机号", "省份", "城市", "邮编", "区号", "运营商"}); err != nil {
		return fmt.Errorf("写入CSV头部失败: %w", err)
	}

	reader := bufio.NewReader(input)

	var totalRows, totalCount, unknownCount int64
	row := make([]string, 0, 6)

	startTime := time.Now()
	// 每 0.5 秒打印一次进度
	printInterval := 500 * time.Millisecond

	stopProgress := make(chan bool)
	progressDone := make(chan bool)

	go func() {
		ticker := time.NewTicker(printInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printProgress(totalRows, totalCount, unknownCount, startTime)
			case <-stopProgress:
				printProgress(totalRows, totalCount, unknownCount, startTime)
				progressDone <- true
				return
			}
		}
	}()

	firstRecord := true
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			close(stopProgress)
			<-progressDone
			return fmt.Errorf("读取文本行失败: %w", err)
		}

		// 去除行尾换行符（兼容 CRLF）
		lineBytes = bytes.TrimRight(lineBytes, "\r\n")
		lineLen := len(lineBytes)

		// 将行计数提前，确保即使 EOF 空行也被计入
		totalRows++

		// 如果是 EOF 且当前行为空，则退出循环
		if err == io.EOF && lineLen == 0 {
			break
		}

		if totalRows == 1 && lineLen > 0 && lineBytes[0] == 0xEF {
			// 跳过 UTF-8 BOM
			if lineLen >= 3 && lineBytes[0] == 0xEF && lineBytes[1] == 0xBB && lineBytes[2] == 0xBF {
				lineBytes = lineBytes[3:]
				lineLen = len(lineBytes)
			}
		}

		// 去除首尾空白
		trimmed := bytes.TrimSpace(lineBytes)
		trimmedLen := len(trimmed)

		if firstRecord {
			if trimmedLen == 0 {
				if err == io.EOF {
					break
				}
				continue
			}
			firstRecord = false
			if isHeaderLine(string(trimmed)) {
				if debug {
					fmt.Println("DEBUG: 跳过CSV/文本头部")
				}
				if err == io.EOF {
					break
				}
				continue
			}
		}

		if trimmedLen == 0 {
			if err == io.EOF {
				break
			}
			continue
		}

		phones := extractPhonesFromBytes(trimmed)
		if len(phones) == 0 {
			if err == io.EOF {
				break
			}
			continue
		}

		for _, phone := range phones {
			totalCount++
			// 提取前7位作为查询key
			phonePrefix := phone[0:7]

			// 查询归属地
			info := db.Query(phonePrefix)

			if info == nil {
				// 未找到，从池中获取默认PhoneInfo
				info = phoneInfoPool.Get().(*PhoneInfo)
				info.Province = "未知"
				info.City = "未知"
				info.ZipCode = ""
				info.AreaCode = ""
				info.CardType = "未知"
				unknownCount++
				if debug {
					fmt.Printf("DEBUG: 未知归属地手机号: %s (第 %d 行)\n", phone, totalRows)
				}
			} else {
				// 找到了，从池中获取PhoneInfo副本
				poolInfo := phoneInfoPool.Get().(*PhoneInfo)
				poolInfo.Province = info.Province
				poolInfo.City = info.City
				poolInfo.ZipCode = info.ZipCode
				poolInfo.AreaCode = info.AreaCode
				poolInfo.CardType = info.CardType
				info = poolInfo
			}

			row = row[:0]
			row = append(row, phone, info.Province, info.City, info.ZipCode, info.AreaCode, info.CardType)
			if err := csvWriter.Write(row); err != nil {
				close(stopProgress)
				<-progressDone
				return fmt.Errorf("写入CSV失败: %w", err)
			}

			ResetPhoneInfo(info)
			phoneInfoPool.Put(info)

			if totalCount%100000 == 0 {
				csvWriter.Flush()
				bufOut.Flush()
			}
		}

		if err == io.EOF {
			break
		}
	}

	close(stopProgress)
	<-progressDone

	csvWriter.Flush()
	bufOut.Flush()

	printFinalStats(totalRows, totalCount, unknownCount, time.Since(startTime), outputFile)
	return nil
}

// printProgress 打印进度信息
func printProgress(totalRows, totalCount, unknownCount int64, startTime time.Time) {
	elapsed := time.Since(startTime)
	if totalCount > 0 {
		rate := float64(totalCount) / elapsed.Seconds()
		fmt.Printf("\r📊 进度: 已处理 %d 行, 查询 %d 个手机号 (%.0f 条/秒) 耗时: %v | 未知归属地: %d",
			totalRows, totalCount, rate, elapsed.Round(time.Second), unknownCount)
	} else if totalRows > 0 {
		fmt.Printf("\r📊 进度: 已处理 %d 行, 等待发现手机号... 耗时: %v",
			totalRows, elapsed.Round(time.Second))
	}
}

// printFinalStats 打印最终统计信息
func printFinalStats(totalRows, totalCount, unknownCount int64, elapsed time.Duration, outputFile string) {
	fmt.Print("\n")
	fmt.Printf("✅ 处理完成！\n")
	fmt.Printf("   总行数: %d 行\n", totalRows)
	fmt.Printf("   总查询数: %d 个手机号\n", totalCount)

	if totalCount > 0 {
		rate := float64(totalCount) / elapsed.Seconds()
		unknownPercent := float64(unknownCount) / float64(totalCount) * 100
		fmt.Printf("   未知归属地: %d 个 (%.2f%%)\n", unknownCount, unknownPercent)
		fmt.Printf("   已知归属地: %d 个 (%.2f%%)\n", totalCount-unknownCount, 100-unknownPercent)
		fmt.Printf("   总耗时: %v\n", elapsed.Round(time.Second))
		fmt.Printf("   平均速度: %.0f 个手机号/秒\n", rate)

		if unknownPercent > 10 {
			fmt.Printf("\n⚠️  提示: 发现 %d 个未知归属地的手机号 (占 %.1f%%)，可能是号段数据库版本较旧\n",
				unknownCount, unknownPercent)
		}
	} else {
		fmt.Printf("   未发现任何有效手机号\n")
		fmt.Printf("   总耗时: %v\n", elapsed.Round(time.Second))
	}

	fmt.Printf("   结果已保存到: %s\n", outputFile)
}
