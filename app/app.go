package app

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/braintree/manners"
	"github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/websocket"
	"github.com/kr/pty"
)

type InitMessage struct {
	Arguments string `json:"Arguments,omitempty"`
	AuthToken string `json:"AuthToken,omitempty"`
}

type App struct {
	commandServer string
	options       *Options

	upgrader *websocket.Upgrader
	server   *manners.GracefulServer

	titleTemplate *template.Template

	timer *time.Timer

	// clientContext writes concurrently
	// Use atomic operations.
	connections *int64
}

type Options struct {
	Address             string                 `hcl:"address"`
	Port                string                 `hcl:"port"`
	WSOrigin            string                 `hcl:"allowed_origin"`
	PermitWrite         bool                   `hcl:"permit_write"`
	EnableBasicAuth     bool                   `hcl:"enable_basic_auth"`
	Credential          string                 `hcl:"credential"`
	EnableRandomUrl     bool                   `hcl:"enable_random_url"`
	RandomUrlLength     int                    `hcl:"random_url_length"`
	IndexFile           string                 `hcl:"index_file"`
	EnableTLS           bool                   `hcl:"enable_tls"`
	TLSCrtFile          string                 `hcl:"tls_crt_file"`
	TLSKeyFile          string                 `hcl:"tls_key_file"`
	EnableTLSClientAuth bool                   `hcl:"enable_tls_client_auth"`
	TLSCACrtFile        string                 `hcl:"tls_ca_crt_file"`
	TitleFormat         string                 `hcl:"title_format"`
	EnableReconnect     bool                   `hcl:"enable_reconnect"`
	ReconnectTime       int                    `hcl:"reconnect_time"`
	MaxConnection       int                    `hcl:"max_connection"`
	Once                bool                   `hcl:"once"`
	Timeout             int                    `hcl:"timeout"`
	PermitArguments     bool                   `hcl:"permit_arguments"`
	CloseSignal         int                    `hcl:"close_signal"`
	Preferences         HtermPrefernces        `hcl:"preferences"`
	RawPreferences      map[string]interface{} `hcl:"preferences"`
	Width               int                    `hcl:"width"`
	Height              int                    `hcl:"height"`
}

var DefaultOptions = Options{
	Address:             "",
	Port:                "8080",
    WSOrigin:            "http://127.0.0.1",
	PermitWrite:         false,
	EnableBasicAuth:     false,
	Credential:          "",
	EnableRandomUrl:     false,
	RandomUrlLength:     8,
	IndexFile:           "",
	EnableTLS:           false,
	TLSCrtFile:          "~/.gotty.crt",
	TLSKeyFile:          "~/.gotty.key",
	EnableTLSClientAuth: false,
	TLSCACrtFile:        "~/.gotty.ca.crt",
	TitleFormat:         "GoTTY",
	EnableReconnect:     false,
	ReconnectTime:       10,
	MaxConnection:       0,
	Once:                false,
	CloseSignal:         1, // syscall.SIGHUP
	Preferences:         HtermPrefernces{},
	Width:               0,
	Height:              0,
}

var Version = "1.0.1"

func New(commandServer string, options *Options) (*App, error) {
	titleTemplate, err := template.New("title").Parse(options.TitleFormat)
	if err != nil {
		return nil, errors.New("Title format string syntax error")
	}
	connections := int64(0)

	var originChecker func(r *http.Request) bool
	if options.WSOrigin != "" {
		originChecker = func(r *http.Request) bool {
			return r.Header.Get("Origin") == options.WSOrigin
		}
    }

	return &App{
		options:       options,
		commandServer: commandServer,

		upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			Subprotocols:    []string{"gotty"},
			CheckOrigin:     originChecker,
		},

		titleTemplate: titleTemplate,

		connections: &connections,
	}, nil
}

func (app *App) Run() error {
	endpoint := net.JoinHostPort(app.options.Address, app.options.Port)
	log.Printf("Server is starting at %s", endpoint)

	handler := http.HandlerFunc(app.handleRequest)
	siteHandler := wrapLogger(handler)
	app.server = app.makeServer(endpoint, &siteHandler)

	err := app.server.ListenAndServe()
	if err != nil {
		return err
	}

	log.Printf("Exiting...")

	return nil
}

func (app *App) makeServer(addr string, handler *http.Handler) *manners.GracefulServer {
	server := &http.Server{
		Addr:    addr,
		Handler: *handler,
	}
	return manners.NewWithServer(server)
}

func (app *App) restartTimer() {
	if app.options.Timeout > 0 {
		app.timer.Reset(time.Duration(app.options.Timeout) * time.Second)
	}
}

func (app *App) readMapping() map[string][]string {
	resp, err := http.Get(app.commandServer)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	var mapping map[string][]string
	json.Unmarshal(body, &mapping)
	return mapping
}

func (app *App) handleAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Write([]byte("var gotty_auth_token = '" + app.options.Credential + "';"))
}

func (app *App) handleWS(command []string, w http.ResponseWriter, r *http.Request) {
	connections := atomic.AddInt64(app.connections, 1)
	if int64(app.options.MaxConnection) != 0 {
		if connections > int64(app.options.MaxConnection) {
			log.Printf("Reached max connection: %d", app.options.MaxConnection)
			return
		}
	}
	log.Printf("New client connected: %s", r.RemoteAddr)

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	conn, err := app.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("Failed to upgrade connection: " + err.Error())
		return
	}

	_, stream, err := conn.ReadMessage()
	if err != nil {
		log.Print("Failed to authenticate websocket connection")
		conn.Close()
		return
	}
	var init InitMessage

	err = json.Unmarshal(stream, &init)
	if err != nil {
		log.Printf("Failed to parse init message %v", err)
		conn.Close()
		return
	}
	argv := command[1:]
	app.server.StartRoutine()

	cmd := exec.Command(command[0], argv...)
	ptyIo, err := pty.Start(cmd)
	if err != nil {
		log.Print("Failed to execute command", err)
		return
	}

	if app.options.MaxConnection != 0 {
		log.Printf("Command is running for client %s with PID %d (args=%q), connections: %d/%d",
			r.RemoteAddr, cmd.Process.Pid, strings.Join(argv, " "), connections, app.options.MaxConnection)
	} else {
		log.Printf("Command is running for client %s with PID %d (args=%q), connections: %d",
			r.RemoteAddr, cmd.Process.Pid, strings.Join(argv, " "), connections)
	}

	context := &clientContext{
		app:        app,
		request:    r,
		connection: conn,
		command:    cmd,
		pty:        ptyIo,
		writeMutex: &sync.Mutex{},
	}

	context.goHandleClient()
}

func (app *App) handleRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(path, "/")
	// TODO: this panics if the path doesn't have enough stuff in it
	// TODO: actually match on /proxy and don't do this strings.Split thing
	prefix := strings.Join(parts[:3], "/")
	// /proxy/ID/
	if strings.HasSuffix(path, "/auth_token.js") {
		app.handleAuthToken(w, r)
	} else if strings.HasSuffix(path, "/ws") {
		id := parts[2]
		mapping := app.readMapping()
		if command, ok := mapping[id]; ok {
			app.handleWS(command, w, r)
		}
	} else {
        handler := http.FileServer(
            &assetfs.AssetFS{Asset: Asset, AssetDir: AssetDir, Prefix: "static"},
        )
        if (app.options.IndexFile != "") {
            handler = http.FileServer(http.Dir(app.options.IndexFile))
        }
        http.StripPrefix(prefix, handler).ServeHTTP(w, r)
	}
}

func (app *App) Exit() (firstCall bool) {
	if app.server != nil {
		firstCall = app.server.Close()
		if firstCall {
			log.Printf("Received Exit command, waiting for all clients to close sessions...")
		}
		return firstCall
	}
	return true
}

func wrapLogger(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWrapper{w, 200}
		handler.ServeHTTP(rw, r)
		log.Printf("%s %d %s %s", r.RemoteAddr, rw.status, r.Method, r.URL.Path)
	})
}
