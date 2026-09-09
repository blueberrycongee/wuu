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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

var ErrUnauthorized = errors.New("invalid credentials or revoked device")
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

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
		if err = os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL);
 INSERT INTO schema_version SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM schema_version);
 CREATE TABLE IF NOT EXISTS accounts(username TEXT PRIMARY KEY,salt BLOB NOT NULL,password BLOB NOT NULL,recovery BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS devices(pub TEXT PRIMARY KEY,account TEXT NOT NULL REFERENCES accounts(username) ON DELETE CASCADE,name TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('host','phone')),added_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(hash BLOB PRIMARY KEY,pub TEXT NOT NULL REFERENCES devices(pub) ON DELETE CASCADE,expires INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS devices_account ON devices(account);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != 1 {
		db.Close()
		return nil, fmt.Errorf("unsupported account database version %d", version)
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
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
		err := s.db.QueryRow(`SELECT salt,password FROM accounts WHERE username=?`, in.Username).Scan(&salt, &hash)
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
		if _, err = tx.Exec(`INSERT INTO accounts VALUES(?,?,?,?)`, in.Username, salt, hash, digest(recovery)); err != nil {
			return Session{}, ErrConflict
		}
	} else {
		// Recheck after password work so a concurrent recovery cannot resurrect a session.
		var current []byte
		if err = tx.QueryRow(`SELECT password FROM accounts WHERE username=?`, in.Username).Scan(&current); err != nil || subtle.ConstantTimeCompare(current, hash) != 1 {
			return Session{}, ErrUnauthorized
		}
	}
	var owner, role string
	err = tx.QueryRow(`SELECT account,role FROM devices WHERE pub=?`, in.Pub).Scan(&owner, &role)
	if err != nil && err != sql.ErrNoRows {
		return Session{}, err
	}
	if err == nil && (owner != in.Username || role != in.Role) {
		return Session{}, ErrConflict
	}
	if _, err = tx.Exec(`INSERT INTO devices VALUES(?,?,?,?,?) ON CONFLICT(pub) DO UPDATE SET name=excluded.name`, in.Pub, in.Username, in.Name, in.Role, time.Now().Unix()); err != nil {
		return Session{}, err
	}
	token := randomToken()
	// One HTTP session per device limits stale credentials and storage growth.
	if _, err = tx.Exec(`DELETE FROM sessions WHERE pub=? OR expires<?`, in.Pub, time.Now().Unix()); err != nil {
		return Session{}, err
	}
	if _, err = tx.Exec(`INSERT INTO sessions VALUES(?,?,?)`, digest(token), in.Pub, time.Now().Add(90*24*time.Hour).Unix()); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return Session{Token: token, Username: in.Username, Pub: in.Pub, Recovery: recovery}, nil
}
func (s *Store) Authenticate(token string) (Device, error) {
	if len(token) != 43 {
		return Device{}, ErrUnauthorized
	}
	var d Device
	err := s.db.QueryRow(`SELECT d.pub,d.account,d.name,d.role,d.added_at FROM sessions s JOIN devices d ON d.pub=s.pub WHERE s.hash=? AND s.expires>?`, digest(token), time.Now().Unix()).Scan(&d.Pub, &d.Account, &d.Name, &d.Role, &d.AddedAt)
	if err != nil {
		return Device{}, ErrUnauthorized
	}
	return d, nil
}
func (s *Store) Device(pub string) (Device, bool) {
	var d Device
	err := s.db.QueryRow(`SELECT pub,account,name,role,added_at FROM devices WHERE pub=?`, pub).Scan(&d.Pub, &d.Account, &d.Name, &d.Role, &d.AddedAt)
	return d, err == nil
}
func (s *Store) Devices(account string) ([]Device, error) {
	rows, err := s.db.Query(`SELECT pub,account,name,role,added_at FROM devices WHERE account=? ORDER BY added_at,pub`, account)
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
	result, err := s.db.Exec(`DELETE FROM devices WHERE account=? AND pub=?`, account, pub)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrUnauthorized
	}
	return nil
}

// Reset changes the password, rotates the single-use recovery secret and removes
// every device and token in the same transaction. Session history remains on hosts.
func (s *Store) Reset(username, secret, password string, recovering bool) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validPassword(password) {
		return "", errors.New("password must be 12-1024 bytes")
	}
	var salt, hash, recoveryHash []byte
	if err := s.db.QueryRow(`SELECT salt,password,recovery FROM accounts WHERE username=?`, username).Scan(&salt, &hash, &recoveryHash); err != nil {
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
	result, err := tx.Exec(`UPDATE accounts SET salt=?,password=?,recovery=? WHERE username=? AND password=? AND recovery=?`, newSalt, newHash, digest(next), username, hash, recoveryHash)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", ErrUnauthorized
	}
	if _, err = tx.Exec(`DELETE FROM devices WHERE account=?`, username); err != nil {
		return "", err
	}
	return next, tx.Commit()
}
