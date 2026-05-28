# PhoneInfo
手机号归属地信息离线查询工具

### 项目结构
<pre>
├── main.go                 # 主程序 - 使用二进制 phone.dat 数据库
├── beta.go                 # 备选程序 - 使用文本格式数据库
├── phones.txt              # 输入文件：待查询的手机号列表
├── phone.dat               # 二进制数据库文件
├── phone2region.txt        # 文本数据库文件
├── result.csv              # 输出文件：查询结果（CSV格式）
└── result.txt              # 输出文件：查询结果（TXT格式，可选）
</pre>

### 初始化 Go 模块
```
go mod init phone-batch
```

### 运行程序
```
go run main.go
```
```
go run main.go --debug
```
