// Package password 提供密码哈希与校验（Argon2id，使用 golang.org/x/crypto）。
//
// 存储格式（PHC 风格字符串）：
//
//	$argon2id$v=19$m=65536,t=3,p=1$<salt-b64>$<hash-b64>
//
// 该格式自描述，将来调整参数不影响旧密码校验。
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params 是 Argon2id 参数。
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams 是默认参数（约 64MB 内存）。
var DefaultParams = Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// ErrMismatch 表示密码不匹配。
var ErrMismatch = errors.New("密码不正确")

// Hash 生成密码哈希。
func Hash(plain string) (string, error) {
	return HashWith(plain, DefaultParams)
}

// HashWith 使用指定参数生成哈希。
func HashWith(plain string, p Params) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", errors.New("密码不能为空")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐失败: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify 校验明文密码是否与编码哈希匹配。
func Verify(plain, encoded string) error {
	p, salt, want, err := decode(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(plain), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

func decode(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, errors.New("密码哈希格式不正确")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, errors.New("密码哈希版本解析失败")
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, errors.New("密码哈希参数解析失败")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, errors.New("密码哈希盐解析失败")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, errors.New("密码哈希内容解析失败")
	}
	return p, salt, key, nil
}
