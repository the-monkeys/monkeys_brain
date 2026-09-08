package models

import (
	"database/sql"
	"time"

	"github.com/the-monkeys/the_monkeys/common/interservice"
)

// type TheMonkeysUser struct {
// 	Id                        int64     `json:"id" gorm:"primaryKey"`
// 	UUID                      string    `json:"unique_id"`
// 	FirstName                 string    `json:"first_name"`
// 	LastName                  string    `json:"last_name"`
// 	Email                     string    `json:"email"`
// 	Password                  string    `json:"password"`
// 	CreateTime                string    `json:"create_time,omitempty"`
// 	UpdateTime                string    `json:"update_time,omitempty"`
// 	IsActive                  bool      `json:"is_active,omitempty"`
// 	Role                      int32     `json:"role,omitempty"`
// 	EmailVerificationToken    string    `json:"email_verification_token"`
// 	EmailVerificationTimeout  time.Time `json:"email_verification_timeout"`
// 	MobileVerificationToken   string    `json:"mobile_verification_token"`
// 	MobileVerificationTimeout time.Time `json:"mobile_verification_timeout"`
// 	Deactivated               bool      `json:"deactivated"`
// 	LoginMethod               string    `json:"login_method"`
// 	EmailVerified             bool      `json:"email_verified"`
// }

// type PasswordReset struct {
// 	Id                int64     `json:"id" gorm:"primaryKey"`
// 	Email             string    `json:"email"`
// 	RecoveryHash      string    `json:"recovery_hash"`
// 	TimeOut           time.Time `json:"time_out"`
// 	LastPasswordReset time.Time `json:"last_pass_reset"`
// }

// ----------------------------------------------------NEW Monkeys----------------------------------------
type TheMonkeysUser struct {
	Id                          int64          `json:"id"`
	AccountId                   string         `json:"account_id"`
	Username                    string         `json:"username"`
	FirstName                   string         `json:"first_name"`
	LastName                    string         `json:"last_name"`
	Email                       string         `json:"email"`
	Password                    string         `json:"password"`
	PasswordVerificationToken   sql.NullString `json:"password_verification_token"`
	PasswordVerificationTimeout sql.NullTime   `json:"password_verification_timeout"`
	EmailVerificationStatus     string         `json:"email_verified"`
	UserStatus                  string         `json:"is_active,omitempty"`
	EmailVerificationToken      string         `json:"email_verification_token"`
	EmailVerificationTimeout    sql.NullTime   `json:"email_verification_timeout"`
	MobileVerificationToken     string         `json:"mobile_verification_token"`
	MobileVerificationTimeout   sql.NullTime   `json:"mobile_verification_timeout"`
	LoginMethod                 string         `json:"login_method"`
	ClientId                    string         `json:"client_id"`
	Client                      string         `json:"client"`
	IpAddress                   string         `json:"ip"`
	Role                        string         `json:"role,omitempty"`
}

type TheMonkeysAccount struct {
	Id        int64  `json:"id"`
	AccountId string `json:"account_id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
}

type TheMonkeysMessage = interservice.Message

// VerificationRequest tracks user verification requests for the checkmark system.
type VerificationRequest struct {
	ID               string         `json:"id"`
	Username         string         `json:"username"`
	VerificationType string         `json:"verification_type"` // "social_proof", "id_document", "professional"
	ProofURLs        string         `json:"proof_urls"`        // JSON array of URLs
	AdditionalInfo   string         `json:"additional_info"`
	Status           string         `json:"status"` // "pending", "approved", "rejected"
	ReviewerUsername sql.NullString `json:"reviewer_username"`
	RejectionReason  sql.NullString `json:"rejection_reason"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	ReviewedAt       sql.NullTime   `json:"reviewed_at"`
}
