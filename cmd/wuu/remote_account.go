package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

// Secrets arrive through stdin, never command arguments, process listings or logs.
func runRemoteAccount(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: wuu remote account <login|register|status|logout|revoke|password|recover> (JSON on stdin)")
	}
	path, err := remoteStorePath()
	if err != nil {
		return err
	}
	name, _ := os.Hostname()
	store, err := host.LoadOrCreateStore(path, name)
	if err != nil {
		return err
	}
	action := args[0]
	ctx := context.Background()
	saved := store.Account()
	if action == "status" {
		if saved == nil {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"account": nil})
		}
		var result map[string]any
		if err = account.Request(ctx, saved.Server, saved.Token, "GET", "/devices", nil, &result); err != nil {
			if !account.Unauthorized(err) {
				return err
			}
			if err = store.SetAccount(nil); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"account": nil})
		}
		result["server"] = saved.Server
		result["pub"] = secure.EncodeKey(store.Identity().Public())
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	var in struct {
		Server   string `json:"server"`
		Username string `json:"username"`
		Password string `json:"password"`
		Secret   string `json:"secret"`
		Pub      string `json:"pub"`
	}
	if action != "logout" {
		if err = json.NewDecoder(io.LimitReader(os.Stdin, 8192)).Decode(&in); err != nil {
			return err
		}
	}
	if action == "login" || action == "register" {
		server, err := account.Origin(in.Server)
		if err != nil {
			return err
		}
		user := strings.ToLower(strings.TrimSpace(in.Username))
		id := store.Identity()
		request := account.Login{Username: user, Password: in.Password, Pub: secure.EncodeKey(id.Public()), Role: "host", Name: store.HostName(), Proof: base64.RawURLEncoding.EncodeToString(id.SignRelayAuth([]byte("wuu/account/enroll/v1:"+user), "host"))}
		var result account.Session
		if err = account.Request(ctx, server, "", "POST", "/"+action, request, &result); err != nil {
			return err
		}
		if err = store.SetAccount(&account.Credentials{Server: server, Token: result.Token, Username: result.Username}); err != nil {
			return err
		}
		if err = store.SetRelayURL("ws" + strings.TrimPrefix(server, "http") + "/v1/connect"); err != nil {
			return err
		}
		// The token belongs to the host and never crosses into the renderer.
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"username": result.Username, "server": server, "recovery": result.Recovery})
	}
	if action == "recover" {
		var result map[string]string
		if err = account.Request(ctx, in.Server, "", "POST", "/recover", map[string]string{"username": in.Username, "secret": in.Secret, "password": in.Password}, &result); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if saved == nil {
		return errors.New("this computer is not logged in")
	}
	switch action {
	case "logout":
		if err = account.Request(ctx, saved.Server, saved.Token, "POST", "/logout", nil, nil); err != nil && !account.Unauthorized(err) {
			return err
		}
		if err = store.SetAccount(nil); err != nil {
			return err
		}
	case "revoke":
		if err = account.Request(ctx, saved.Server, saved.Token, "DELETE", "/devices/"+in.Pub, nil, nil); err != nil {
			return err
		}
	case "password":
		var result map[string]string
		if err = account.Request(ctx, saved.Server, saved.Token, "POST", "/password", map[string]string{"username": saved.Username, "secret": in.Secret, "password": in.Password}, &result); err != nil {
			return err
		}
		if err = store.SetAccount(nil); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	default:
		return errors.New("unknown account action")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"ok": true})
}
