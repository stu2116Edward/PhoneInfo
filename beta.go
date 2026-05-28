package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 查询结果结构
type QueryResult struct {
	Phone    string
	Province string
	City     string
	ZipCode  string
	AreaCode string
	CardType string
	Success  bool
	ErrorMsg string
}

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

func main() {
	// 解析命令行参数
	debugFlag := flag.Bool("debug", false, "输出调试信息（状态和错误信息）")
	flag.Parse()
	debug = *debugFlag

	// 配置参数
	inputFile := "phones.txt"
	outputFile := "result.csv"
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

// processPhonesStreaming 流式处理手机号文件
func processPhonesStreaming(inputFile, outputFile string, db *PhoneDatabase) error {
	// 打开输入文件
	input, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("打开输入文件失败: %w", err)
	}
	defer input.Close()

	// 创建输出文件
	output, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer output.Close()

	// 创建CSV写入器
	csvWriter := csv.NewWriter(output)
	defer csvWriter.Flush()

	// 写入CSV头部
	var headers []string
	if debug {
		headers = []string{"手机号", "省份", "城市", "邮编", "区号", "运营商", "状态", "错误信息"}
	} else {
		headers = []string{"手机号", "省份", "城市", "邮编", "区号", "运营商"}
	}
	if err := csvWriter.Write(headers); err != nil {
		return fmt.Errorf("写入CSV头部失败: %w", err)
	}

	// 编译手机号正则表达式（支持11位数字）
	phoneRegex := regexp.MustCompile(`1[3-9]\d{9}`)

	// 创建带缓冲的读取器
	reader := bufio.NewReader(input)

	var totalCount, successCount, failCount int

	// 逐行读取并处理
	for lineNum := 1; ; lineNum++ {
		// 读取一行
		line, err := reader.ReadString('\n')
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("读取第%d行失败: %w", lineNum, err)
		}

		// 去除行尾换行符并清理空格
		currentLine := strings.TrimSpace(line)

		// 跳过空行和注释行
		if currentLine == "" || strings.HasPrefix(currentLine, "#") {
			continue
		}

		// 使用正则表达式提取手机号
		matches := phoneRegex.FindAllString(currentLine, -1)
		if len(matches) == 0 {
			if debug {
				fmt.Printf("⚠️ 第%d行未找到有效手机号: %s\n", lineNum, currentLine[:min(len(currentLine), 50)])
			}
			continue
		}

		// 处理找到的所有手机号（去重）
		uniquePhones := make(map[string]bool)
		for _, phone := range matches {
			// 验证手机号格式
			if len(phone) == 11 && isNumeric(phone) {
				uniquePhones[phone] = true
			}
		}

		// 查询每个手机号
		for phone := range uniquePhones {
			totalCount++
			fmt.Printf("🔍 正在查询 [%d]: %s ", totalCount, phone)

			result := QueryResult{Phone: phone}

			// 提取前7位作为查询key
			phonePrefix := phone[0:7]

			// 查询归属地
			info := db.Query(phonePrefix)
			if info != nil {
				result.Success = true
				result.Province = info.Province
				result.City = info.City
				result.ZipCode = info.ZipCode
				result.AreaCode = info.AreaCode
				result.CardType = info.CardType
				successCount++
				fmt.Printf(" ✅ %s %s %s 邮编:%s 区号:%s\n",
					info.Province, info.City, info.CardType, info.ZipCode, info.AreaCode)
			} else {
				result.Success = false
				result.ErrorMsg = fmt.Sprintf("未找到号段 %s 的归属地", phonePrefix)
				failCount++
				fmt.Printf(" ❌ 未找到号段 %s\n", phonePrefix)
			}

			// 写入CSV
			var row []string
			if debug {
				row = []string{
					result.Phone, result.Province, result.City, result.ZipCode,
					result.AreaCode, result.CardType,
					map[bool]string{true: "成功", false: "失败"}[result.Success],
					result.ErrorMsg,
				}
			} else {
				row = []string{
					result.Phone, result.Province, result.City, result.ZipCode,
					result.AreaCode, result.CardType,
				}
			}
			if err := csvWriter.Write(row); err != nil {
				return fmt.Errorf("写入结果失败: %w", err)
			}

			// 定期刷新缓冲区
			if totalCount%100 == 0 {
				csvWriter.Flush()
			}

			// 避免请求过快
			time.Sleep(10 * time.Millisecond)
		}

		// 每处理100行打印一次进度
		if lineNum%100 == 0 {
			fmt.Printf("\n📊 进度: 已处理 %d 行, 查询 %d 个手机号, 成功 %d, 失败 %d\n",
				lineNum, totalCount, successCount, failCount)
		}
	}

	// 打印统计信息
	fmt.Println("\n📊 查询统计:")
	fmt.Printf("  总查询数: %d\n", totalCount)
	fmt.Printf("  ✅ 成功: %d\n", successCount)
	fmt.Printf("  ❌ 失败: %d\n", failCount)
	fmt.Printf("  📄 结果已保存到: %s\n", outputFile)

	return nil
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

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
