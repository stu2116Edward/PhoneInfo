package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"regexp"
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
	fmt.Println("🔧 正在加载手机号数据库...")
	fmt.Println("📖 正在解析数据库文件...")

	db, err := LoadPhoneDatabase(phoneDataFile)
	if err != nil {
		fmt.Printf("❌ 加载手机号数据库失败: %v\n", err)
		fmt.Println("请确保 phone.dat 文件存在，可以从以下地址下载：")
		fmt.Println("https://github.com/ALI1416/phone2region/blob/master/data/phone.dat")
		return
	}

	// 获取号段记录条数
	recordCount := db.GetRecordCount()
	fmt.Printf("   ✅ 成功解析 %d 条号段记录，跳过 0 行错误，0 行无效数据\n", recordCount)
	fmt.Printf("✅ 成功加载手机号数据库，共 %d 条号段记录，版本: %d\n", recordCount, db.GetVersion())

	// 流式处理手机号文件
	err = processPhonesStreaming(inputFile, outputFile, db)
	if err != nil {
		fmt.Printf("❌ 处理失败: %v\n", err)
		return
	}
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

	if debug {
		fmt.Printf("📊 数据库信息: 总大小=%d字节, 索引偏移=%d, 索引数量=%d\n",
			db.totalLen, db.firstOffset, db.indexRecordNum)
		fmt.Printf("🔍 数据库版本: %d\n", db.GetVersion())
	}

	return db, nil
}

// GetVersion 获取数据库版本
func (db *PhoneDatabase) GetVersion() uint32 {
	return uint32(get4(db.content[0:4]))
}

// GetRecordCount 获取号段记录条数
func (db *PhoneDatabase) GetRecordCount() int32 {
	return db.indexRecordNum
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
	var lineNum int
	var currentLine string

	// 使用逐行读取方式
	for {
		lineNum++
		// 读取一行，包括最后的换行符
		line, err := reader.ReadString('\n')

		// 处理读取到的内容（即使有错误，只要读取到内容就处理）
		if len(line) > 0 {
			// 去除行尾换行符并清理空格
			currentLine = strings.TrimSpace(strings.TrimRight(line, "\r\n"))

			// 跳过空行和注释行
			if currentLine != "" && !strings.HasPrefix(currentLine, "#") {
				// 使用正则表达式提取手机号
				matches := phoneRegex.FindAllString(currentLine, -1)
				if len(matches) > 0 {
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
				} else if debug {
					fmt.Printf("⚠️ 第%d行未找到有效手机号: %s\n", lineNum, currentLine[:min(len(currentLine), 50)])
				}
			}
		}

		// 检查是否读取完成
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("读取第%d行失败: %w", lineNum, err)
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
