package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"
)

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
	CTCC_v: "中国电信",
	CUCC_v: "中国联通",
	CMCC_v: "中国移动",
	CBCC_v: "中国广电",
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
	outputFile := "result.csv"
	phoneDataFile := "phone.dat"

	// 加载 phone.dat 文件
	fmt.Println("正在加载手机号数据库...")

	db, err := LoadPhoneDatabase(phoneDataFile)
	if err != nil {
		fmt.Printf("加载手机号数据库失败: %v\n", err)
		fmt.Println("请确保 phone.dat 文件存在，可以从以下地址下载：")
		fmt.Println("https://github.com/ALI1416/phone2region/blob/master/data/phone.dat")
		return
	}

	// 获取号段记录条数
	recordCount := db.GetRecordCount()
	fmt.Printf("成功加载手机号数据库，共 %d 条号段记录\n", recordCount)

	// 流式处理手机号文件
	err = processPhonesStreaming(inputFile, outputFile, db)
	if err != nil {
		fmt.Printf("处理失败: %v\n", err)
	}
}

// LoadPhoneDatabase 加载 phone.dat 文件
func LoadPhoneDatabase(filename string) (*PhoneDatabase, error) {
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
	db.indexRecordNum = (db.totalLen - db.firstOffset) / 9

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

// get4 从字节数组中读取4字节整数（小端序）
func get4(b []byte) int32 {
	if len(b) < 4 {
		return 0
	}
	return int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16 | int32(b[3])<<24
}

// prefixOfDigits 计算手机号前7位数值
func prefixOfDigits[T ~string | ~[]byte](value T) int32 {
	var prefix int32
	for i := 0; i < 7; i++ {
		prefix = prefix*10 + int32(value[i]-'0')
	}
	return prefix
}

// QueryBytes 直接使用字节切片查询，零拷贝，不产生 string 分配
func (db *PhoneDatabase) QueryBytes(phone []byte) *PhoneInfo {
	return db.queryPrefix(prefixOfDigits(phone))
}

// QueryString 直接使用字符串查询，避免字节切片分配
func (db *PhoneDatabase) QueryString(phone string) *PhoneInfo {
	return db.queryPrefix(prefixOfDigits(phone))
}

// queryPrefix 根据前缀数值查询，复用查询逻辑
func (db *PhoneDatabase) queryPrefix(targetPrefix int32) *PhoneInfo {
	// 二分查找
	left, right := int32(0), db.indexRecordNum-1
	for left <= right {
		mid := (left + right) >> 1
		offset := db.firstOffset + mid*9

		curPhone := get4(db.content[offset : offset+4])
		recordOffset := get4(db.content[offset+4 : offset+8])
		cardType := db.content[offset+8]

		if curPhone < targetPrefix {
			left = mid + 1
		} else if curPhone > targetPrefix {
			right = mid - 1
		} else {
			// 找到匹配，解析记录区
			endOffset := recordOffset
			for db.content[endOffset] != 0 {
				endOffset++
			}

			// 直接解析字段
			record := db.content[recordOffset:endOffset]
			var fields [4][]byte
			fieldIdx := 0
			start := 0
			for i := 0; i < len(record) && fieldIdx < 4; i++ {
				if record[i] == '|' {
					if i > start {
						fields[fieldIdx] = record[start:i]
					}
					fieldIdx++
					start = i + 1
				}
			}
			if start < len(record) && fieldIdx < 4 {
				fields[fieldIdx] = record[start:]
			}

			// 从池中获取 PhoneInfo
			info := phoneInfoPool.Get().(*PhoneInfo)
			info.Province = unsafeString(fields[0])
			info.City = unsafeString(fields[1])
			info.ZipCode = unsafeString(fields[2])
			info.AreaCode = unsafeString(fields[3])
			if name, ok := cardTypeMap[cardType]; ok {
				info.CardType = name
			} else {
				info.CardType = "中国电信"
			}
			return info
		}
	}

	// 默认返回（未知归属地）
	info := phoneInfoPool.Get().(*PhoneInfo)
	info.Province = "未知"
	info.City = "未知"
	info.ZipCode = "000000"
	info.AreaCode = "0000"
	info.CardType = "中国电信"
	return info
}

// unsafeString 将字节切片零拷贝转换为字符串
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
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
	return info.Province == "未知" || info.City == "未知"
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
			// 使用 QueryString 避免字节切片分配
			info := db.QueryString(phone)

			if isUnknown(info) {
				unknownCount++
				if debug {
					fmt.Printf("DEBUG: 未知归属地手机号: %s (第 %d 行)\n", phone, totalRows)
				}
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
