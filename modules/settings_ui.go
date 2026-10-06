package modules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func launchURL(address string) error {
	program, args := "xdg-open", []string{address}
	if runtime.GOOS == "windows" {
		program = "rundll32.exe"
		args = []string{"url.dll,FileProtocolHandler", address}
	} else if runtime.GOOS == "darwin" {
		program = "open"
	}
	cmd := exec.Command(program, args...)
	configureChild(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

const settingsPage = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>discord-vlc-rpc settings</title><style>body{font:16px system-ui;max-width:760px;margin:40px auto;padding:0 24px;background:#17191e;color:#eee}input,textarea,select{font:inherit;box-sizing:border-box;max-width:100%;background:#272a32;color:#fff;border:1px solid #59606e;padding:8px;border-radius:5px}label{display:block;margin:18px 0 6px}input[type=text],input[type=password],input[type=number],textarea{width:100%}button{background:#f47e21;border:0;border-radius:6px;padding:12px 24px;font:inherit;margin:24px 0;cursor:pointer}small{color:#b9bec8}</style></head><body><h1>discord-vlc-rpc</h1><p>{{.Message}}</p><form method="post" action="/settings"><input type="hidden" name="csrf" value="{{.Token}}"><label><input type="checkbox" name="enabled" {{if .Config.Enabled}}checked{{end}}> Enable Discord presence</label><label>Discord application ID</label><input name="appid" type="text" value="{{.Config.ApplicationID}}"><label>TMDb API key or read access token</label><input name="apikey" type="password" autocomplete="new-password" placeholder="Leave blank to keep the saved key"><small>The saved key is never displayed.</small><label><input name="clear_key" type="checkbox"> Remove saved TMDb key</label><label>TMDb language</label><input name="language" type="text" value="{{.Config.Language}}"><label><input type="checkbox" name="episode" {{if .Config.EpisodeLookup}}checked{{end}}> Look up episode names, counts and images</label><label>Metadata cache lifetime (days)</label><input name="days" type="number" min="1" max="3650" value="{{.Config.CacheDays}}"><label><input type="checkbox" name="index" {{if .Config.IndexEnabled}}checked{{end}}> Build and maintain the local title index</label><small>Requires a TMDb key. Downloads daily ID exports when enabled with a key.</small><label>Database folder (blank uses the default)</label><input name="indexpath" type="text" value="{{.Config.IndexPath}}"><label>Metadata cache folder (blank uses the default)</label><input name="cachepath" type="text" value="{{.Config.CachePath}}"><label>Poster fit</label><select name="fit"><option value="contain" {{if eq .Config.PosterFit "contain"}}selected{{end}}>Square through wsrv.nl</option><option value="raw" {{if eq .Config.PosterFit "raw"}}selected{{end}}>Original TMDb image</option></select><label>Ignored local files or folders (one per line)</label><textarea name="ignored" rows="5">{{.Ignored}}</textarea><label>Fallback large image asset</label><input name="largeimage" type="text" value="{{.Config.LargeImage}}"><label>Large image hover text</label><input name="largetext" type="text" value="{{.Config.LargeText}}"><label>Playing badge asset</label><input name="playing" type="text" value="{{.Config.SmallPlaying}}"><label>Paused badge asset</label><input name="paused" type="text" value="{{.Config.SmallPaused}}"><label>Idle badge asset</label><input name="idle" type="text" value="{{.Config.SmallIdle}}"><button>Save settings</button></form><p>Changes apply automatically. Use the tray to configure VLC HTTP or clear the cache.</p></body></html>`

func settingsHandler(p paths, token, host string, onSaved ...func()) http.Handler {
	page := template.Must(template.New("settings").Parse(settingsPage))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		if r.Host != host {
			http.Error(w, "Invalid host", 403)
			return
		}
		if r.URL.Path == "/open" && r.URL.Query().Get("token") == token {
			http.SetCookie(w, &http.Cookie{Name: "tracker-session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/settings", 303)
			return
		}
		cookie, err := r.Cookie("tracker-session")
		if err != nil || cookie.Value != token {
			http.Error(w, "Open Settings from the tray", 403)
			return
		}
		if r.URL.Path != "/settings" {
			http.NotFound(w, r)
			return
		}
		c, err := loadSettings(p)
		if err != nil {
			http.Error(w, "Cannot read saved settings", 500)
			return
		}
		message := "Settings for the standalone Go companion."
		if r.Method == "POST" {
			r.Body = http.MaxBytesReader(w, r.Body, 65536)
			if r.ParseForm() != nil || r.FormValue("csrf") != token || r.Header.Get("Origin") != "http://"+host {
				http.Error(w, "Invalid settings request", 403)
				return
			}
			c.Enabled = r.FormValue("enabled") == "on"
			c.ApplicationID = r.FormValue("appid")
			if r.FormValue("clear_key") == "on" {
				c.APIKey = ""
			} else if key := strings.TrimSpace(r.FormValue("apikey")); key != "" {
				c.APIKey = key
			}
			c.Language = r.FormValue("language")
			c.EpisodeLookup = r.FormValue("episode") == "on"
			c.CacheDays, _ = strconv.Atoi(r.FormValue("days"))
			c.IndexEnabled = r.FormValue("index") == "on"
			c.IndexPath = strings.TrimSpace(r.FormValue("indexpath"))
			c.CachePath = strings.TrimSpace(r.FormValue("cachepath"))
			c.PosterFit = r.FormValue("fit")
			c.Ignored = nil
			for _, line := range strings.Split(r.FormValue("ignored"), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					c.Ignored = append(c.Ignored, line)
				}
			}
			c.LargeImage = r.FormValue("largeimage")
			c.LargeText = r.FormValue("largetext")
			c.SmallPlaying = r.FormValue("playing")
			c.SmallPaused = r.FormValue("paused")
			c.SmallIdle = r.FormValue("idle")
			if err = saveSettings(p, c); err != nil {
				message = err.Error()
			} else {
				message = "Settings saved."
				if len(onSaved) > 0 && onSaved[0] != nil {
					onSaved[0]()
				}
			}
		} else if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, struct {
			Config                  settings
			Token, Message, Ignored string
		}{c, token, message, strings.Join(c.Ignored, "\n")})
	})
}
func startSettingsUI(ctx context.Context, p paths, onSaved ...func()) (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		listener.Close()
		return "", err
	}
	token := hex.EncodeToString(secret)
	host := listener.Addr().String()
	server := &http.Server{Handler: settingsHandler(p, token, host, onSaved...), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = server.Serve(listener) }()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	return "http://" + host + "/open?token=" + url.QueryEscape(token), nil
}
