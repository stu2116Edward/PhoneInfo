package main

import (
	"bufio"
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// 增强的查询结果结构（包含邮编和区号）
type QueryResult struct {
	Phone    string
	Province string
	City     string
	ZipCode  string // 邮政编码
	AreaCode string // 区号
	CardType string
	Success  bool
	ErrorMsg string
}

// phone.dat 文件的元数据
type PhoneDataHeader struct {
	Version          uint32 // 版本号
	FirstIndexOffset uint32 // 第一个索引的偏移量
}

// 索引记录
type IndexRecord struct {
	PhonePrefix uint32 // 手机号前7位（数字）
	Offset      uint32 // 记录区的偏移
	CardType    byte   // 卡类型
}

// 全局调试标志
var debug bool

func main() {
	// 解析命令行参数
	debugFlag := flag.Bool("debug", false, "输出调试信息（状态和错误信息）")
	flag.Parse()
	debug = *debugFlag

	// 配置参数
	inputFile := "phones.txt"    // 输入文件路径
	outputFile := "result.csv"   // 输出文件路径
	phoneDataFile := "phone.dat" // phone.dat 文件路径

	// 加载 phone.dat 文件
	db, err := LoadPhoneDatabase(phoneDataFile)
	if err != nil {
		fmt.Printf("❌ 加载手机号数据库失败: %v\n", err)
		fmt.Println("请确保 phone.dat 文件存在，可以从以下地址下载：")
		fmt.Println("https://github.com/ALI1416/phone2region/blob/master/data/phone.dat")
		return
	}
	fmt.Printf("✅ 成功加载手机号数据库，版本: %d\n", db.header.Version)

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
			// 查询归属地（包含邮编和区号）
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

		// 避免请求过快（可选）
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

// PhoneDatabase 手机号数据库
type PhoneDatabase struct {
	file         []byte          // 文件内容
	header       PhoneDataHeader // 文件头
	indexRecords []IndexRecord   // 索引记录
}

// PhoneInfo 查询结果
type PhoneInfo struct {
	Province string
	City     string
	ZipCode  string
	AreaCode string
	CardType string
}

// LoadPhoneDatabase 加载 phone.dat 文件
func LoadPhoneDatabase(filename string) (*PhoneDatabase, error) {
	// 读取文件
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	if len(data) < 8 {
		return nil, fmt.Errorf("文件太小，格式错误")
	}

	db := &PhoneDatabase{
		file: data,
	}

	// 解析文件头 (前8字节)
	db.header.Version = binary.LittleEndian.Uint32(data[0:4])
	db.header.FirstIndexOffset = binary.LittleEndian.Uint32(data[4:8])

	// 计算索引区大小
	if db.header.FirstIndexOffset >= uint32(len(data)) {
		return nil, fmt.Errorf("索引偏移量超出文件范围")
	}

	indexStart := db.header.FirstIndexOffset
	remainingSize := uint32(len(data)) - indexStart

	// 每个索引记录：手机号前7位(4字节二进制) + 记录偏移(4字节) + 卡类型(1字节) = 9字节
	// 注意：标准的 phone.dat 格式是 4+4+1=9字节
	const indexRecordSize = 9
	numRecords := remainingSize / indexRecordSize

	fmt.Printf("📊 数据库信息: 总大小=%d字节, 索引偏移=%d, 索引数量=%d\n",
		len(data), indexStart, numRecords)

	db.indexRecords = make([]IndexRecord, 0, numRecords)

	for i := uint32(0); i < numRecords; i++ {
		offset := indexStart + i*indexRecordSize

		// 确保不越界
		if int(offset+indexRecordSize) > len(data) {
			break
		}

		// 读取手机号前7位（4字节整数，大端序或小端序？尝试小端序）
		phonePrefix := binary.LittleEndian.Uint32(data[offset : offset+4])

		// 读取记录区偏移（4字节）
		recordOffset := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		// 读取卡类型（1字节）
		cardType := data[offset+8]

		db.indexRecords = append(db.indexRecords, IndexRecord{
			PhonePrefix: phonePrefix,
			Offset:      recordOffset,
			CardType:    cardType,
		})
	}

	fmt.Printf("✅ 成功加载 %d 条索引记录\n", len(db.indexRecords))

	// 调试：显示前几条索引记录
	if debug && len(db.indexRecords) > 0 {
		fmt.Printf("🔍 前5条索引记录:\n")
		for i := 0; i < 5 && i < len(db.indexRecords); i++ {
			fmt.Printf("  [%d] 前缀=%d (字符串:%07d) 偏移=%d 类型=%d\n",
				i, db.indexRecords[i].PhonePrefix, db.indexRecords[i].PhonePrefix,
				db.indexRecords[i].Offset, db.indexRecords[i].CardType)
		}
	}

	return db, nil
}

// Query 查询手机号归属地
func (db *PhoneDatabase) Query(phone string) (*PhoneInfo, error) {
	if len(phone) != 11 {
		return nil, fmt.Errorf("手机号必须为11位")
	}

	// 将手机号前7位转换为数字
	phonePrefixStr := phone[0:7]
	phonePrefix, err := strconv.ParseUint(phonePrefixStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("无效的手机号前缀: %s", phonePrefixStr)
	}

	targetPrefix := uint32(phonePrefix)

	if debug {
		fmt.Printf("\n[DEBUG] 查询前缀: %s -> %d\n", phonePrefixStr, targetPrefix)
	}

	// 二分查找索引
	left, right := 0, len(db.indexRecords)-1
	var found *IndexRecord

	for left <= right {
		mid := (left + right) / 2
		record := db.indexRecords[mid]

		if record.PhonePrefix < targetPrefix {
			left = mid + 1
		} else if record.PhonePrefix > targetPrefix {
			right = mid - 1
		} else {
			found = &record
			break
		}
	}

	if found == nil {
		// 如果找不到精确匹配，尝试找最接近的前缀
		if debug {
			fmt.Printf("[DEBUG] 未找到精确匹配，尝试模糊查询...\n")
			// 显示附近的前缀
			for i := 0; i < len(db.indexRecords); i++ {
				if db.indexRecords[i].PhonePrefix > targetPrefix {
					if i > 0 {
						fmt.Printf("[DEBUG] 附近前缀: %d (索引%d) 和 %d (索引%d)\n",
							db.indexRecords[i-1].PhonePrefix, i-1,
							db.indexRecords[i].PhonePrefix, i)
					}
					break
				}
			}
		}
		return nil, fmt.Errorf("未找到该手机号的归属地信息")
	}

	if debug {
		fmt.Printf("[DEBUG] 找到索引: 前缀=%d, 偏移=%d\n", found.PhonePrefix, found.Offset)
	}

	// 从记录区读取归属地信息
	recordStart := found.Offset
	recordEnd := recordStart

	// 查找字符串结束符 '\0'
	for recordEnd < uint32(len(db.file)) && db.file[recordEnd] != 0 {
		recordEnd++
	}

	if recordEnd >= uint32(len(db.file)) {
		return nil, fmt.Errorf("记录格式错误")
	}

	record := string(db.file[recordStart:recordEnd])
	parts := strings.Split(record, "|")

	if len(parts) < 4 {
		return nil, fmt.Errorf("记录格式错误: %s", record)
	}

	// 解析卡类型
	cardTypeName := getCardTypeName(found.CardType)

	return &PhoneInfo{
		Province: parts[0],
		City:     parts[1],
		ZipCode:  parts[2],
		AreaCode: parts[3],
		CardType: cardTypeName,
	}, nil
}

// getCardTypeName 获取运营商名称
func getCardTypeName(cardType byte) string {
	switch cardType {
	case 0:
		return "中国移动"
	case 1:
		return "中国联通"
	case 2:
		return "中国电信"
	default:
		return "未知运营商"
	}
}

// 从文件读取手机号（支持多种格式）
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
		// 跳过空行和注释行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 处理可能包含逗号、制表符、空格分隔的情况
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

// 提取手机号（去除空格、特殊字符等）
func extractPhoneNumber(s string) string {
	// 只保留数字
	digits := ""
	for _, char := range s {
		if char >= '0' && char <= '9' {
			digits += string(char)
		}
	}

	// 如果是11位数字，返回
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

	// 根据文件扩展名选择格式
	if strings.HasSuffix(filename, ".csv") {
		return exportCSV(results, file)
	} else {
		return exportTXT(results, file)
	}
}

// 导出CSV格式
func exportCSV(results []QueryResult, file *os.File) error {
	writer := csv.NewWriter(file)
	defer writer.Flush()

	// 根据debug标志决定表头和数据
	var headers []string
	if debug {
		headers = []string{"手机号", "省份", "城市", "邮编", "区号", "运营商", "状态", "错误信息"}
	} else {
		headers = []string{"手机号", "省份", "城市", "邮编", "区号", "运营商"}
	}

	if err := writer.Write(headers); err != nil {
		return err
	}

	// 写入数据
	for _, r := range results {
		var row []string
		if debug {
			row = []string{
				r.Phone,
				r.Province,
				r.City,
				r.ZipCode,
				r.AreaCode,
				r.CardType,
				map[bool]string{true: "成功", false: "失败"}[r.Success],
				r.ErrorMsg,
			}
		} else {
			row = []string{
				r.Phone,
				r.Province,
				r.City,
				r.ZipCode,
				r.AreaCode,
				r.CardType,
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
	// 根据debug标志决定表头和数据格式
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

	// 写入数据
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
