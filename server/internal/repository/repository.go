// Package repository 是纯数据访问层：只做增删改查，**不写业务逻辑**。
//
// 分层约定（D77.2）：handler → service → repository。
// **handler 不得出现 *gorm.DB**。
package repository

import (
	"errors"

	"gorm.io/gorm"
)

// ErrNotFound 由各 repo 在「查不到」时返回（调用方用 errors.Is 判断）。
var ErrNotFound = errors.New("记录不存在")

// wrap 把 gorm 的 ErrRecordNotFound 归一化为 ErrNotFound。
func wrap(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// IsNotFound 判断错误是否为「记录不存在」。
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
