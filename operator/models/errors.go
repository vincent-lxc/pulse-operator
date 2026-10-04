// 本文件提供可安全返回给调用方的公开错误，不包含数据库原文。
package models

import (
	"errors"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
)

// NewValidationError 创建参数校验错误。
func NewValidationError(message string) error {
	return servertypes.NewPublicError(servertypes.ErrorKindValidation, servertypes.PublicCodeValidation, message, errors.New(message))
}

// NewBusinessError 创建业务规则错误。
func NewBusinessError(message string) error {
	return servertypes.NewPublicError(servertypes.ErrorKindBusiness, servertypes.PublicCodeBusiness, message, errors.New(message))
}

// NewNotFoundError 创建 404。message 会返回给调用方。
func NewNotFoundError(message string) error {
	return servertypes.NewPublicError(servertypes.ErrorKindNotFound, servertypes.PublicCodeNotFound, message, errors.New(message))
}
