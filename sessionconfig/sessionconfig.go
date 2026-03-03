package sessionconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/apresolve"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
	"github.com/elxgy/go-librespot/session"
)

const defaultCallbackPort = 8080

type Options struct {
	ConfigDir    string
	CallbackPort int
	DeviceType   string
	ClientToken  string
	ClientId     string
	Credentials  any
}

func ParseDeviceType(val string) (devicespb.DeviceType, error) {
	if val == "" {
		val = "computer"
	}
	key := strings.ToUpper(val)
	enum, ok := devicespb.DeviceType_value[key]
	if !ok {
		return 0, fmt.Errorf("invalid device type: %s", val)
	}
	return devicespb.DeviceType(enum), nil
}

func NewSessionFromConfigDir(ctx context.Context, log librespot.Logger, opts Options) (*session.Session, *librespot.AppState, error) {
	if opts.ConfigDir == "" {
		return nil, nil, fmt.Errorf("config dir required")
	}
	if err := os.MkdirAll(opts.ConfigDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("config dir: %w", err)
	}

	deviceType, err := ParseDeviceType(opts.DeviceType)
	if err != nil {
		return nil, nil, err
	}

	appState := &librespot.AppState{}
	appState.SetLogger(log)
	if err := appState.Read(opts.ConfigDir); err != nil {
		return nil, nil, fmt.Errorf("read app state: %w", err)
	}

	if appState.DeviceId == "" {
		b := make([]byte, 20)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("generate device id: %w", err)
		}
		appState.DeviceId = hex.EncodeToString(b)
		if err := appState.Write(); err != nil {
			return nil, nil, fmt.Errorf("persist device id: %w", err)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resolver := apresolve.NewApResolver(log, client)

	callbackPort := opts.CallbackPort
	if callbackPort <= 0 {
		callbackPort = defaultCallbackPort
	}

	var creds any
	if opts.Credentials != nil {
		creds = opts.Credentials
	} else if len(appState.Credentials.Data) > 0 {
		creds = session.StoredCredentials{
			Username: appState.Credentials.Username,
			Data:     appState.Credentials.Data,
		}
	} else {
		creds = session.InteractiveCredentials{CallbackPort: callbackPort}
	}

	sessOpts := &session.Options{
		Log:         log,
		DeviceType:  deviceType,
		DeviceId:    appState.DeviceId,
		ClientId:    opts.ClientId,
		Credentials: creds,
		ClientToken: opts.ClientToken,
		Resolver:    resolver,
		Client:      client,
		AppState:    appState,
	}

	sess, err := session.NewSessionFromOptions(ctx, sessOpts)
	if err != nil {
		return nil, nil, err
	}

	if _, isInteractive := creds.(session.InteractiveCredentials); isInteractive {
		appState.Credentials.Username = sess.Username()
		appState.Credentials.Data = sess.StoredCredentials()
		if err := appState.Write(); err != nil {
			sess.Close()
			return nil, nil, fmt.Errorf("persist credentials: %w", err)
		}
		log.WithField("username", librespot.ObfuscateUsername(sess.Username())).Debugf("stored credentials")
	}

	return sess, appState, nil
}
