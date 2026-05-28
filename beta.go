package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
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

	// 读取手机号文件
	phones, err := readPhonesFromFile(inputFile)
	if err != nil {
		fmt.Printf("❌ 读取文件失败: %v\n", err)
		return
	}

	fmt.Printf("\n📱 共读取到 %d 个手机号\n", len(phones))

	// 批量查询
	results := make([]QueryResult, 0, len(phones))
	successCount := 0
	failCount := 0

	for i, phone := range phones {
		fmt.Printf("🔍 正在查询 [%d/%d]: %s ", i+1, len(phones), phone)

		result := QueryResult{Phone: phone}

		// 验证手机号格式
		if len(phone) != 11 || !isNumeric(phone) {
			result.Success = false
			result.ErrorMsg = "无效的手机号格式"
			failCount++
			fmt.Println(" ❌ 格式错误")
		} else {
			// 提取前7位作为查询key
			phonePrefix := phone[0:7]

			// 查询归属地（使用二分查找）
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
		}

		results = append(results, result)
		time.Sleep(10 * time.Millisecond)
	}

	// 导出结果
	err = exportResults(results, outputFile)
	if err != nil {
		fmt.Printf("❌ 导出失败: %v\n", err)
		return
	}

	// 打印统计信息
	fmt.Println("\n📊 查询统计:")
	fmt.Printf("  总数: %d\n", len(phones))
	fmt.Printf("  ✅ 成功: %d\n", successCount)
	fmt.Printf("  ❌ 失败: %d\n", failCount)
	fmt.Printf("  📄 结果已保存到: %s\n", outputFile)
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
	// 例如：1380000 可能对应 138 开头的号段
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

// 从文件读取手机号
func readPhonesFromFile(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var phones []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == '\t' || r == ' '
		})

		if len(parts) > 0 {
			phone := extractPhoneNumber(parts[0])
			if phone != "" {
				phones = append(phones, phone)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return phones, nil
}

// 提取手机号
func extractPhoneNumber(s string) string {
	digits := ""
	for _, char := range s {
		if char >= '0' && char <= '9' {
			digits += string(char)
		}
	}

	if len(digits) == 11 {
		return digits
	}
	return ""
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

// 导出结果
func exportResults(results []QueryResult, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	if strings.HasSuffix(filename, ".csv") {
		return exportCSV(results, file)
	}
	return exportTXT(results, file)
}

// 导出CSV格式
func exportCSV(results []QueryResult, file *os.File) error {
	writer := csv.NewWriter(file)
	defer writer.Flush()

	var headers []string
	if debug {
		headers = []string{"手机号", "省份", "城市", "邮编", "区号", "运营商", "状态", "错误信息"}
	} else {
		headers = []string{"手机号", "省份", "城市", "邮编", "区号", "运营商"}
	}

	if err := writer.Write(headers); err != nil {
		return err
	}

	for _, r := range results {
		var row []string
		if debug {
			row = []string{
				r.Phone, r.Province, r.City, r.ZipCode, r.AreaCode, r.CardType,
				map[bool]string{true: "成功", false: "失败"}[r.Success],
				r.ErrorMsg,
			}
		} else {
			row = []string{
				r.Phone, r.Province, r.City, r.ZipCode, r.AreaCode, r.CardType,
			}
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// 导出TXT格式
func exportTXT(results []QueryResult, file *os.File) error {
	var header string
	var dataFormat string

	if debug {
		header = "手机号\t省份\t城市\t邮编\t区号\t运营商\t状态\t错误信息\n"
		dataFormat = "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n"
	} else {
		header = "手机号\t省份\t城市\t邮编\t区号\t运营商\n"
		dataFormat = "%s\t%s\t%s\t%s\t%s\t%s\n"
	}

	if _, err := file.WriteString(header); err != nil {
		return err
	}

	for _, r := range results {
		var line string
		if debug {
			line = fmt.Sprintf(dataFormat,
				r.Phone, r.Province, r.City, r.ZipCode, r.AreaCode, r.CardType,
				map[bool]string{true: "成功", false: "失败"}[r.Success],
				r.ErrorMsg)
		} else {
			line = fmt.Sprintf(dataFormat,
				r.Phone, r.Province, r.City, r.ZipCode, r.AreaCode, r.CardType)
		}
		if _, err := file.WriteString(line); err != nil {
			return err
		}
	}
	return nil
}
