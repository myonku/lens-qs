package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv 从若干候选路径里**第一个存在**的文件读取 KEY=VALUE，
// 写入进程环境变量，供随后的 env() 读取。
//
// 设计要点：
//   - **不覆盖已存在的环境变量**：真实环境变量（含容器注入、命令行前置变量）优先；
//   - 支持 `#` 注释、空行、`export KEY=VALUE`、值两端的单/双引号；
//   - 文件不存在或读取失败都**静默跳过**，绝不因为缺 .env 而启动失败；
//   - 刻意不引入第三方依赖（本项目只需要这几行）。
func LoadDotEnv(paths ...string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		loadFrom(f)
		_ = f.Close()
		return // 只用第一个找到的文件
	}
}

func loadFrom(f *os.File) {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// 去掉行尾注释（仅当值没有被引号包裹时）
		if len(val) > 0 && val[0] != '"' && val[0] != '\'' {
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		// 真实环境变量优先，不覆盖
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
