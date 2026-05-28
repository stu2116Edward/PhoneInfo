package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
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

// 运营商类型常量
const (
	CMCC   byte = 1 // 中国移动
	CUCC   byte = 2 // 中国联通
	CTCC   byte = 3 // 中国电信
	CTCC_v byte = 4 // 电信虚拟运营商
	CUCC_v byte = 5 // 联通虚拟运营商
	CMCC_v byte = 6 // 移动虚拟运营商
	CBCC   byte = 7 // 中国广电
	CBCC_v byte = 8 // 广电虚拟运营商
)

// 运营商名称映射
var cardTypeMap = map[byte]string{
	CMCC:   "中国移动",
	CUCC:   "中国联通",
	CTCC:   "中国电信",
	CBCC:   "中国广电",
	CTCC_v: "中国电信虚拟运营商",
	CUCC_v: "中国联通虚拟运营商",
	CMCC_v: "中国移动虚拟运营商",
	CBCC_v: "中国广电虚拟运营商",
}

// PhoneDatabase 手机号数据库
type PhoneDatabase struct {
	content        []byte // 文件内容
	totalLen       int32  // 文件总长度
	firstOffset    int32  // 第一个索引的偏移量
	indexRecordNum int32  // 索引记录数量
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
	phoneDataFile := "phone.dat"

	// 加载 phone.dat 文件
	db, err := LoadPhoneDatabase(phoneDataFile)
	if err != nil {
		fmt.Printf("❌ 加载手机号数据库失败: %v\n", err)
		fmt.Println("请确保 phone.dat 文件存在，可以从以下地址下载：")
		fmt.Println("https://github.com/ALI1416/phone2region/blob/master/data/phone.dat")
		return
	}
	fmt.Printf("✅ 成功加载手机号数据库，版本: %d\n", db.GetVersion())

	// 1. 读取文件中的手机号
	phones, err := readPhonesFromFile(inputFile)
	if err != nil {
		fmt.Printf("❌ 读取文件失败: %v\n", err)
		return
	}

	fmt.Printf("📱 共读取到 %d 个手机号\n", len(phones))

	// 2. 批量查询
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
			// 查询归属地
			info, err := db.Query(phone)
			if err != nil {
				result.Success = false
				result.ErrorMsg = err.Error()
				failCount++
				fmt.Printf(" ❌ 查询失败: %v\n", err)
			} else {
				result.Success = true
				result.Province = info.Province
				result.City = info.City
				result.ZipCode = info.ZipCode
				result.AreaCode = info.AreaCode
				result.CardType = info.CardType
				successCount++
				fmt.Printf(" ✅ %s %s %s 邮编:%s 区号:%s\n",
					info.Province, info.City, info.CardType, info.ZipCode, info.AreaCode)
			}
		}

		results = append(results, result)
		time.Sleep(10 * time.Millisecond)
	}

	// 3. 导出结果
	err = exportResults(results, outputFile)
	if err != nil {
		fmt.Printf("❌ 导出失败: %v\n", err)
		return
	}

	// 4. 打印统计信息
	fmt.Println("\n📊 查询统计:")
	fmt.Printf("  总数: %d\n", len(phones))
	fmt.Printf("  ✅ 成功: %d\n", successCount)
	fmt.Printf("  ❌ 失败: %d\n", failCount)
	fmt.Printf("  📄 结果已保存到: %s\n", outputFile)
}

// LoadPhoneDatabase 加载 phone.dat 文件
func LoadPhoneDatabase(filename string) (*PhoneDatabase, error) {
	// 读取文件
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	if len(content) < 8 {
		return nil, fmt.Errorf("文件太小，格式错误")
	}

	db := &PhoneDatabase{
		content:  content,
		totalLen: int32(len(content)),
	}

	// 获取第一个索引的偏移量（从第4字节开始，占4字节）
	db.firstOffset = get4(content[4:8])

	// 计算索引记录数量
	db.indexRecordNum = (db.totalLen - db.firstOffset) / 9

	fmt.Printf("📊 数据库信息: 总大小=%d字节, 索引偏移=%d, 索引数量=%d\n",
		db.totalLen, db.firstOffset, db.indexRecordNum)

	if debug {
		fmt.Printf("🔍 数据库版本: %d\n", db.GetVersion())
	}

	return db, nil
}

// GetVersion 获取数据库版本
func (db *PhoneDatabase) GetVersion() uint32 {
	return uint32(get4(db.content[0:4]))
}

// get4 从字节数组中读取4字节整数
func get4(b []byte) int32 {
	if len(b) < 4 {
		return 0
	}
	return int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16 | int32(b[3])<<24
}

// getN 将字符串数字转换为uint32
func getN(s string) (uint32, error) {
	var n, cutoff, maxVal uint32
	base := 10
	cutoff = (1<<32-1)/10 + 1
	maxVal = 1<<uint(32) - 1

	for i := 0; i < len(s); i++ {
		var v byte
		d := s[i]
		switch {
		case '0' <= d && d <= '9':
			v = d - '0'
		case 'a' <= d && d <= 'z':
			v = d - 'a' + 10
		case 'A' <= d && d <= 'Z':
			v = d - 'A' + 10
		default:
			return 0, fmt.Errorf("invalid syntax")
		}
		if v >= byte(base) {
			return 0, fmt.Errorf("invalid syntax")
		}

		if n >= cutoff {
			n = (1<<32 - 1)
			return n, fmt.Errorf("value out of range")
		}
		n *= uint32(base)

		n1 := n + uint32(v)
		if n1 < n || n1 > maxVal {
			n = (1<<32 - 1)
			return n, fmt.Errorf("value out of range")
		}
		n = n1
	}
	return n, nil
}

// Query 查询手机号归属地
func (db *PhoneDatabase) Query(phone string) (*PhoneInfo, error) {
	if len(phone) < 7 || len(phone) > 11 {
		return nil, fmt.Errorf("手机号长度必须在7-11位之间")
	}

	// 获取手机号前7位并转换为数字
	phonePrefixStr := phone[0:7]
	phonePrefix, err := getN(phonePrefixStr)
	if err != nil {
		return nil, fmt.Errorf("无效的手机号前缀: %s", phonePrefixStr)
	}
	targetPrefix := int32(phonePrefix)

	if debug {
		fmt.Printf("\n[DEBUG] 查询前缀: %s -> %d\n", phonePrefixStr, targetPrefix)
	}

	// 二分查找
	var left, right int32
	right = db.indexRecordNum - 1

	for left <= right {
		mid := (left + right) / 2
		offset := db.firstOffset + mid*9

		if offset >= db.totalLen {
			break
		}

		// 读取当前索引中的手机号前缀
		curPhone := get4(db.content[offset : offset+4])
		// 读取记录区偏移
		recordOffset := get4(db.content[offset+4 : offset+8])
		// 读取卡类型
		cardType := db.content[offset+8]

		switch {
		case curPhone > targetPrefix:
			right = mid - 1
		case curPhone < targetPrefix:
			left = mid + 1
		default:
			// 找到匹配，解析记录区
			if debug {
				fmt.Printf("[DEBUG] 找到索引: 前缀=%d, 偏移=%d, 类型=%d\n", curPhone, recordOffset, cardType)
			}

			// 查找字符串结束符位置
			endOffset := recordOffset
			for endOffset < db.totalLen && db.content[endOffset] != 0 {
				endOffset++
			}

			if endOffset >= db.totalLen {
				return nil, fmt.Errorf("记录格式错误")
			}

			// 分割记录数据
			recordData := db.content[recordOffset:endOffset]
			parts := bytesSplit(recordData, []byte("|"))

			if len(parts) < 4 {
				return nil, fmt.Errorf("记录格式错误")
			}

			// 获取运营商名称
			cardTypeName, ok := cardTypeMap[cardType]
			if !ok {
				cardTypeName = "未知运营商"
			}

			return &PhoneInfo{
				Province: string(parts[0]),
				City:     string(parts[1]),
				ZipCode:  string(parts[2]),
				AreaCode: string(parts[3]),
				CardType: cardTypeName,
			}, nil
		}
	}

	return nil, fmt.Errorf("未找到该手机号的归属地信息")
}

// bytesSplit 分割字节数组
func bytesSplit(data []byte, sep []byte) [][]byte {
	var result [][]byte
	start := 0
	for i := 0; i <= len(data)-len(sep); i++ {
		if bytesEqual(data[i:i+len(sep)], sep) {
			result = append(result, data[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, data[start:])
	return result
}

// bytesEqual 比较两个字节数组是否相等
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
