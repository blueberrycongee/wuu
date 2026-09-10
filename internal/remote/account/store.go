// Package account owns self-hosted identities and device membership. The account
// operator is a trusted identity authority; application content stays on hosts.
package account

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/argon2"
)

var ErrUnauthorized = errors.New("invalid credentials or revoked device")
var ErrUnavailable = errors.New("account database unavailable")
var ErrConflict = errors.New("account or device already registered")
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)
var enc = base64.RawURLEncoding

type Store struct{ db *sql.DB }
type Device struct {
	Pub     string `json:"pub"`
	Account string `json:"account"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	AddedAt int64  `json:"added_at"`
	Online  bool   `json:"online"`
}
type Login struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Pub      string `json:"pub"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Proof    string `json:"proof"`
}
type Session struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Pub      string `json:"pub"`
	Recovery string `json:"recovery,omitempty"`
}

func (s *Store) Close() error { return s.db.Close() }

type PushRegistration struct {
	Platform string `json:"platform"`
	Token    string `json:"token"`
}

func (s *Store) SetPush(pub string, registration PushRegistration) error {
	device, ok := s.Device(pub)
	if !ok || device.Role != "phone" {
		return ErrUnauthorized
	}
	if registration.Token == "" {
		_, err := s.db.Exec(`DELETE FROM push_devices WHERE pub=$1`, pub)
		return err
	}
	if len(registration.Token) > 4096 || (registration.Platform != "ios" && registration.Platform != "android") {
		return errors.New("invalid push registration")
	}
	_, err := s.db.Exec(`INSERT INTO push_devices(pub,platform,token) VALUES($1,$2,$3) ON CONFLICT(pub) DO UPDATE SET platform=excluded.platform,token=excluded.token`, pub, registration.Platform, registration.Token)
	return err
}

func (s *Store) Push(account, pub string) (PushRegistration, bool) {
	var result PushRegistration
	err := s.db.QueryRow(`SELECT p.platform,p.token FROM push_devices p JOIN devices d ON d.pub=p.pub WHERE d.pub=$1 AND d.account=$2 AND d.role='phone'`, pub, account).Scan(&result.Platform, &result.Token)
	return result, err == nil
}
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return enc.EncodeToString(b)
}
func digest(s string) []byte { v := sha256.Sum256([]byte(s)); return v[:] }
func passwordHash(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
}
func validPassword(p string) bool { return len(p) >= 12 && len(p) <= 1024 }
func validateLogin(in *Login) error {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	pub, err := secure.DecodeKey(in.Pub)
	if err != nil || secure.EncodeKey(pub) != in.Pub {
		return ErrUnauthorized
	}
	sig, err := enc.DecodeString(in.Proof)
	if err != nil || !secure.VerifyRelayAuth(pub, []byte("wuu/account/enroll/v1:"+in.Username), sig, in.Role) {
		return ErrUnauthorized
	}
	if !usernamePattern.MatchString(in.Username) || !validPassword(in.Password) || (in.Role != "host" && in.Role != "phone") || len(in.Name) > 128 {
		return errors.New("username must be 3-64 lowercase letters, digits, dot, dash or underscore; password must be 12-1024 bytes; device role and name must be valid")
	}
	return nil
}
func (s *Store) Login(in Login, register bool) (Session, error) {
	if err := validateLogin(&in); err != nil {
		return Session{}, err
	}
	var salt, hash []byte
	var recovery string
	if register {
		salt = make([]byte, 16)
		_, _ = rand.Read(salt)
		hash = passwordHash(in.Password, salt)
		recovery = randomToken()
	} else {
		err := s.db.QueryRow(`SELECT salt,password FROM accounts WHERE username=$1`, in.Username).Scan(&salt, &hash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrUnavailable
		}
		if err != nil {
			salt = make([]byte, 16)
			hash = make([]byte, 32)
		}
		candidate := passwordHash(in.Password, salt)
		if subtle.ConstantTimeCompare(candidate, hash) != 1 || err != nil {
			return Session{}, ErrUnauthorized
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	if register {
		if _, err = tx.Exec(`INSERT INTO accounts VALUES($1,$2,$3,$4)`, in.Username, salt, hash, digest(recovery)); err != nil {
			return Session{}, conflictError(err)
		}
	} else {
		// Recheck after password work so a concurrent recovery cannot resurrect a session.
		var current []byte
		err = tx.QueryRow(`SELECT password FROM accounts WHERE username=$1 FOR UPDATE`, in.Username).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrUnavailable
		}
		if err != nil || subtle.ConstantTimeCompare(current, hash) != 1 {
			return Session{}, ErrUnauthorized
		}
	}
	return s.enroll(tx, in, recovery)
}

// enroll is shared by password and OAuth authentication; both retain the same
// device ownership checks and locally generated signing keys.
func (s *Store) enroll(tx *sql.Tx, in Login, recovery string) (Session, error) {
	var err error
	var owner, role string
	err = tx.QueryRow(`SELECT account,role FROM devices WHERE pub=$1`, in.Pub).Scan(&owner, &role)
	if err != nil && err != sql.ErrNoRows {
		return Session{}, err
	}
	if err == nil && (owner != in.Username || role != in.Role) {
		return Session{}, ErrConflict
	}
	// The ownership predicate also protects concurrent enrollments from different accounts.
	var enrolled string
	if err = tx.QueryRow(`INSERT INTO devices VALUES($1,$2,$3,$4,$5) ON CONFLICT(pub) DO UPDATE SET name=excluded.name WHERE devices.account=excluded.account AND devices.role=excluded.role RETURNING pub`, in.Pub, in.Username, in.Name, in.Role, time.Now().Unix()).Scan(&enrolled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrConflict
		}
		return Session{}, conflictError(err)
	}
	token := randomToken()
	// One HTTP session per device limits stale credentials and storage growth.
	if _, err = tx.Exec(`DELETE FROM sessions WHERE pub=$1 OR expires<$2`, in.Pub, time.Now().Unix()); err != nil {
		return Session{}, err
	}
	if _, err = tx.Exec(`INSERT INTO sessions VALUES($1,$2,$3)`, digest(token), in.Pub, time.Now().Add(90*24*time.Hour).Unix()); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return Session{Token: token, Username: in.Username, Pub: in.Pub, Recovery: recovery}, nil
}

func (s *Store) githubAccount(subject string, register bool, displayName ...string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback()
	// Serialize first logins for the immutable provider subject, not its mutable handle.
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "github:"+subject); err != nil {
		return "", ErrUnavailable
	}
	var username string
	err = tx.QueryRow(`SELECT account FROM account_identities WHERE provider='github' AND subject=$1`, subject).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		if !register {
			return "", errors.New("registration is disabled by this server")
		}
		username = fmt.Sprintf("gh-%x", digest(randomToken())[:12])
		// OAuth-only accounts have no password or recovery credential.
		if _, err = tx.Exec(`INSERT INTO accounts VALUES($1,''::bytea,''::bytea,''::bytea)`, username); err != nil {
			return "", ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO account_identities(provider,subject,account) VALUES('github',$1,$2)`, subject, username); err != nil {
			return "", ErrUnavailable
		}
	} else if err != nil {
		return "", ErrUnavailable
	}
	if len(displayName) > 0 && len(displayName[0]) <= 100 {
		if _, err = tx.Exec(`UPDATE account_identities SET display_name=$1 WHERE provider='github' AND subject=$2`, displayName[0], subject); err != nil {
			return "", ErrUnavailable
		}
	}
	return username, tx.Commit()
}

func (s *Store) githubLogin(in Login) (Session, error) {
	// Reuse canonical key, role and enrollment-signature validation without
	// introducing a usable password on the OAuth account.
	in.Password = "oauth-validation-only"
	if err := validateLogin(&in); err != nil {
		return Session{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Session{}, ErrUnavailable
	}
	defer tx.Rollback()
	var username string
	if err = tx.QueryRow(`SELECT a.username FROM accounts a JOIN account_identities i ON a.username=i.account WHERE a.username=$1 AND i.provider='github' FOR UPDATE OF a`, in.Username).Scan(&username); err != nil {
		return Session{}, ErrUnauthorized
	}
	return s.enroll(tx, in, "")
}

func (s *Store) AuthMethod(username string) string {
	var provider string
	if s.db.QueryRow(`SELECT provider FROM account_identities WHERE account=$1`, username).Scan(&provider) == nil {
		return provider
	}
	return "password"
}
func (s *Store) Authenticate(token string) (Device, error) {
	if len(token) != 43 {
		return Device{}, ErrUnauthorized
	}
	var d Device
	err := s.db.QueryRow(`SELECT d.pub,d.account,d.name,d.role,d.added_at FROM sessions s JOIN devices d ON d.pub=s.pub WHERE s.hash=$1 AND s.expires>$2`, digest(token), time.Now().Unix()).Scan(&d.Pub, &d.Account, &d.Name, &d.Role, &d.AddedAt)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return Device{}, ErrUnavailable
		}
		return Device{}, ErrUnauthorized
	}
	return d, nil
}
func (s *Store) Device(pub string) (Device, bool) {
	var d Device
	err := s.db.QueryRow(`SELECT pub,account,name,role,added_at FROM devices WHERE pub=$1`, pub).Scan(&d.Pub, &d.Account, &d.Name, &d.Role, &d.AddedAt)
	return d, err == nil
}
func (s *Store) Devices(account string) ([]Device, error) {
	rows, err := s.db.Query(`SELECT pub,account,name,role,added_at FROM devices WHERE account=$1 ORDER BY added_at,pub`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err = rows.Scan(&d.Pub, &d.Account, &d.Name, &d.Role, &d.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(account, pub string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Match Login/Reset lock order so revocation is atomic with device enrollment.
	var owner string
	if err := tx.QueryRow(`SELECT username FROM accounts WHERE username=$1 FOR UPDATE`, account).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnauthorized
		}
		return err
	}
	result, err := tx.Exec(`DELETE FROM devices WHERE account=$1 AND pub=$2`, account, pub)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrUnauthorized
	}
	return tx.Commit()
}

// Reset changes the password, rotates the single-use recovery secret and removes
// every device and token in the same transaction. Session history remains on hosts.
func (s *Store) Reset(username, secret, password string, recovering bool) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validPassword(password) {
		return "", errors.New("password must be 12-1024 bytes")
	}
	var salt, hash, recoveryHash []byte
	if err := s.db.QueryRow(`SELECT salt,password,recovery FROM accounts WHERE username=$1`, username).Scan(&salt, &hash, &recoveryHash); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return "", ErrUnavailable
		}
		return "", ErrUnauthorized
	}
	if recovering {
		if len(secret) != 43 || subtle.ConstantTimeCompare(digest(secret), recoveryHash) != 1 {
			return "", ErrUnauthorized
		}
	} else {
		if len(secret) > 1024 || subtle.ConstantTimeCompare(passwordHash(secret, salt), hash) != 1 {
			return "", ErrUnauthorized
		}
	}
	next := randomToken()
	newSalt := make([]byte, 16)
	_, _ = rand.Read(newSalt)
	newHash := passwordHash(password, newSalt)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE accounts SET salt=$1,password=$2,recovery=$3 WHERE username=$4 AND password=$5 AND recovery=$6`, newSalt, newHash, digest(next), username, hash, recoveryHash)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", ErrUnauthorized
	}
	if _, err = tx.Exec(`DELETE FROM devices WHERE account=$1`, username); err != nil {
		return "", err
	}
	return next, tx.Commit()
}

func conflictError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func (s *Store) DisplayName(username string) string {
	var name string
	if s.db.QueryRow(`SELECT display_name FROM account_identities WHERE account=$1`, username).Scan(&name) == nil && name != "" {
		return name
	}
	return username
}
