package store

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 计算成本，12 是当下推荐的安全值（约 300ms/次）
const bcryptCost = 12

// minPasswordLen 最小密码长度
const minPasswordLen = 6

// HashPassword 使用 bcrypt 对密码做哈希
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword 验证密码是否匹配哈希
func VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePassword 验证密码复杂度：长度至少 6 个字符
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return fmt.Errorf("密码长度至少 %d 个字符", minPasswordLen)
	}
	return nil
}

// hashPassword 包内使用的别名
func hashPassword(password string) (string, error) {
	return HashPassword(password)
}
