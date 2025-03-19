// Code generated by protoc-gen-validate. DO NOT EDIT.
// source: envoy/extensions/http/original_ip_detection/xff/v3/xff.proto

package xffv3

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/types/known/anypb"
)

// ensure the imports are used
var (
	_ = bytes.MinRead
	_ = errors.New("")
	_ = fmt.Print
	_ = utf8.UTFMax
	_ = (*regexp.Regexp)(nil)
	_ = (*strings.Reader)(nil)
	_ = net.IPv4len
	_ = time.Duration(0)
	_ = (*url.URL)(nil)
	_ = (*mail.Address)(nil)
	_ = anypb.Any{}
	_ = sort.Sort
)

// Validate checks the field values on XffConfig with the rules defined in the
// proto definition for this message. If any rules are violated, the first
// error encountered is returned, or nil if there are no violations.
func (m *XffConfig) Validate() error {
	return m.validate(false)
}

// ValidateAll checks the field values on XffConfig with the rules defined in
// the proto definition for this message. If any rules are violated, the
// result is a list of violation errors wrapped in XffConfigMultiError, or nil
// if none found.
func (m *XffConfig) ValidateAll() error {
	return m.validate(true)
}

func (m *XffConfig) validate(all bool) error {
	if m == nil {
		return nil
	}

	var errors []error

	// no validation rules for XffNumTrustedHops

	if all {
		switch v := interface{}(m.GetXffTrustedCidrs()).(type) {
		case interface{ ValidateAll() error }:
			if err := v.ValidateAll(); err != nil {
				errors = append(errors, XffConfigValidationError{
					field:  "XffTrustedCidrs",
					reason: "embedded message failed validation",
					cause:  err,
				})
			}
		case interface{ Validate() error }:
			if err := v.Validate(); err != nil {
				errors = append(errors, XffConfigValidationError{
					field:  "XffTrustedCidrs",
					reason: "embedded message failed validation",
					cause:  err,
				})
			}
		}
	} else if v, ok := interface{}(m.GetXffTrustedCidrs()).(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return XffConfigValidationError{
				field:  "XffTrustedCidrs",
				reason: "embedded message failed validation",
				cause:  err,
			}
		}
	}

	if all {
		switch v := interface{}(m.GetSkipXffAppend()).(type) {
		case interface{ ValidateAll() error }:
			if err := v.ValidateAll(); err != nil {
				errors = append(errors, XffConfigValidationError{
					field:  "SkipXffAppend",
					reason: "embedded message failed validation",
					cause:  err,
				})
			}
		case interface{ Validate() error }:
			if err := v.Validate(); err != nil {
				errors = append(errors, XffConfigValidationError{
					field:  "SkipXffAppend",
					reason: "embedded message failed validation",
					cause:  err,
				})
			}
		}
	} else if v, ok := interface{}(m.GetSkipXffAppend()).(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return XffConfigValidationError{
				field:  "SkipXffAppend",
				reason: "embedded message failed validation",
				cause:  err,
			}
		}
	}

	if len(errors) > 0 {
		return XffConfigMultiError(errors)
	}

	return nil
}

// XffConfigMultiError is an error wrapping multiple validation errors returned
// by XffConfig.ValidateAll() if the designated constraints aren't met.
type XffConfigMultiError []error

// Error returns a concatenation of all the error messages it wraps.
func (m XffConfigMultiError) Error() string {
	var msgs []string
	for _, err := range m {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// AllErrors returns a list of validation violation errors.
func (m XffConfigMultiError) AllErrors() []error { return m }

// XffConfigValidationError is the validation error returned by
// XffConfig.Validate if the designated constraints aren't met.
type XffConfigValidationError struct {
	field  string
	reason string
	cause  error
	key    bool
}

// Field function returns field value.
func (e XffConfigValidationError) Field() string { return e.field }

// Reason function returns reason value.
func (e XffConfigValidationError) Reason() string { return e.reason }

// Cause function returns cause value.
func (e XffConfigValidationError) Cause() error { return e.cause }

// Key function returns key value.
func (e XffConfigValidationError) Key() bool { return e.key }

// ErrorName returns error name.
func (e XffConfigValidationError) ErrorName() string { return "XffConfigValidationError" }

// Error satisfies the builtin error interface
func (e XffConfigValidationError) Error() string {
	cause := ""
	if e.cause != nil {
		cause = fmt.Sprintf(" | caused by: %v", e.cause)
	}

	key := ""
	if e.key {
		key = "key for "
	}

	return fmt.Sprintf(
		"invalid %sXffConfig.%s: %s%s",
		key,
		e.field,
		e.reason,
		cause)
}

var _ error = XffConfigValidationError{}

var _ interface {
	Field() string
	Reason() string
	Key() bool
	Cause() error
	ErrorName() string
} = XffConfigValidationError{}

// Validate checks the field values on XffTrustedCidrs with the rules defined
// in the proto definition for this message. If any rules are violated, the
// first error encountered is returned, or nil if there are no violations.
func (m *XffTrustedCidrs) Validate() error {
	return m.validate(false)
}

// ValidateAll checks the field values on XffTrustedCidrs with the rules
// defined in the proto definition for this message. If any rules are
// violated, the result is a list of violation errors wrapped in
// XffTrustedCidrsMultiError, or nil if none found.
func (m *XffTrustedCidrs) ValidateAll() error {
	return m.validate(true)
}

func (m *XffTrustedCidrs) validate(all bool) error {
	if m == nil {
		return nil
	}

	var errors []error

	for idx, item := range m.GetCidrs() {
		_, _ = idx, item

		if all {
			switch v := interface{}(item).(type) {
			case interface{ ValidateAll() error }:
				if err := v.ValidateAll(); err != nil {
					errors = append(errors, XffTrustedCidrsValidationError{
						field:  fmt.Sprintf("Cidrs[%v]", idx),
						reason: "embedded message failed validation",
						cause:  err,
					})
				}
			case interface{ Validate() error }:
				if err := v.Validate(); err != nil {
					errors = append(errors, XffTrustedCidrsValidationError{
						field:  fmt.Sprintf("Cidrs[%v]", idx),
						reason: "embedded message failed validation",
						cause:  err,
					})
				}
			}
		} else if v, ok := interface{}(item).(interface{ Validate() error }); ok {
			if err := v.Validate(); err != nil {
				return XffTrustedCidrsValidationError{
					field:  fmt.Sprintf("Cidrs[%v]", idx),
					reason: "embedded message failed validation",
					cause:  err,
				}
			}
		}

	}

	if len(errors) > 0 {
		return XffTrustedCidrsMultiError(errors)
	}

	return nil
}

// XffTrustedCidrsMultiError is an error wrapping multiple validation errors
// returned by XffTrustedCidrs.ValidateAll() if the designated constraints
// aren't met.
type XffTrustedCidrsMultiError []error

// Error returns a concatenation of all the error messages it wraps.
func (m XffTrustedCidrsMultiError) Error() string {
	var msgs []string
	for _, err := range m {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// AllErrors returns a list of validation violation errors.
func (m XffTrustedCidrsMultiError) AllErrors() []error { return m }

// XffTrustedCidrsValidationError is the validation error returned by
// XffTrustedCidrs.Validate if the designated constraints aren't met.
type XffTrustedCidrsValidationError struct {
	field  string
	reason string
	cause  error
	key    bool
}

// Field function returns field value.
func (e XffTrustedCidrsValidationError) Field() string { return e.field }

// Reason function returns reason value.
func (e XffTrustedCidrsValidationError) Reason() string { return e.reason }

// Cause function returns cause value.
func (e XffTrustedCidrsValidationError) Cause() error { return e.cause }

// Key function returns key value.
func (e XffTrustedCidrsValidationError) Key() bool { return e.key }

// ErrorName returns error name.
func (e XffTrustedCidrsValidationError) ErrorName() string { return "XffTrustedCidrsValidationError" }

// Error satisfies the builtin error interface
func (e XffTrustedCidrsValidationError) Error() string {
	cause := ""
	if e.cause != nil {
		cause = fmt.Sprintf(" | caused by: %v", e.cause)
	}

	key := ""
	if e.key {
		key = "key for "
	}

	return fmt.Sprintf(
		"invalid %sXffTrustedCidrs.%s: %s%s",
		key,
		e.field,
		e.reason,
		cause)
}

var _ error = XffTrustedCidrsValidationError{}

var _ interface {
	Field() string
	Reason() string
	Key() bool
	Cause() error
	ErrorName() string
} = XffTrustedCidrsValidationError{}
