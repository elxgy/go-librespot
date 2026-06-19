package go_librespot

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

var defaultAuthKeywords = []string{
	"complete authentication",
	"visit the following link",
}

type LogrusAdapter struct {
	Log *logrus.Entry
}

func (l LogrusAdapter) Tracef(format string, args ...any) { l.Log.Tracef(format, args...) }
func (l LogrusAdapter) Debugf(format string, args ...any) { l.Log.Debugf(format, args...) }
func (l LogrusAdapter) Infof(format string, args ...any)  { l.Log.Infof(format, args...) }
func (l LogrusAdapter) Warnf(format string, args ...any)  { l.Log.Warnf(format, args...) }
func (l LogrusAdapter) Errorf(format string, args ...any) { l.Log.Errorf(format, args...) }
func (l LogrusAdapter) Trace(args ...any)                 { l.Log.Trace(args...) }
func (l LogrusAdapter) Debug(args ...any)                 { l.Log.Debug(args...) }
func (l LogrusAdapter) Info(args ...any)                  { l.Log.Info(args...) }
func (l LogrusAdapter) Warn(args ...any)                  { l.Log.Warn(args...) }
func (l LogrusAdapter) Error(args ...any)                 { l.Log.Error(args...) }

func (l LogrusAdapter) WithField(key string, value any) Logger {
	return LogrusAdapter{Log: l.Log.WithField(key, value)}
}

func (l LogrusAdapter) WithError(err error) Logger {
	return LogrusAdapter{Log: l.Log.WithError(err)}
}

type LogrusAdapterWithStderrAuth struct {
	Log          *logrus.Entry
	StderrPrefix string
	AuthKeywords []string
}

func (l LogrusAdapterWithStderrAuth) stderrIfMatch(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	kws := l.AuthKeywords
	if len(kws) == 0 {
		kws = defaultAuthKeywords
	}
	for _, kw := range kws {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(kw)) {
			prefix := l.StderrPrefix
			if prefix == "" {
				prefix = "[librespot] "
			}
			fmt.Fprintln(os.Stderr, prefix+msg)
			return
		}
	}
}

func (l LogrusAdapterWithStderrAuth) Tracef(format string, args ...any) {
	l.Log.Tracef(format, args...)
}
func (l LogrusAdapterWithStderrAuth) Debugf(format string, args ...any) {
	l.Log.Debugf(format, args...)
}
func (l LogrusAdapterWithStderrAuth) Infof(format string, args ...any) {
	l.stderrIfMatch(format, args...)
	l.Log.Infof(format, args...)
}
func (l LogrusAdapterWithStderrAuth) Warnf(format string, args ...any) {
	l.stderrIfMatch(format, args...)
	l.Log.Warnf(format, args...)
}
func (l LogrusAdapterWithStderrAuth) Errorf(format string, args ...any) {
	l.Log.Errorf(format, args...)
}
func (l LogrusAdapterWithStderrAuth) Trace(args ...any) { l.Log.Trace(args...) }
func (l LogrusAdapterWithStderrAuth) Debug(args ...any) { l.Log.Debug(args...) }
func (l LogrusAdapterWithStderrAuth) Info(args ...any) {
	l.stderrIfMatch("%s", fmt.Sprint(args...))
	l.Log.Info(args...)
}
func (l LogrusAdapterWithStderrAuth) Warn(args ...any) {
	l.stderrIfMatch("%s", fmt.Sprint(args...))
	l.Log.Warn(args...)
}
func (l LogrusAdapterWithStderrAuth) Error(args ...any) { l.Log.Error(args...) }

func (l LogrusAdapterWithStderrAuth) WithField(key string, value any) Logger {
	return LogrusAdapterWithStderrAuth{
		Log:          l.Log.WithField(key, value),
		StderrPrefix: l.StderrPrefix,
		AuthKeywords: l.AuthKeywords,
	}
}

func (l LogrusAdapterWithStderrAuth) WithError(err error) Logger {
	return LogrusAdapterWithStderrAuth{
		Log:          l.Log.WithError(err),
		StderrPrefix: l.StderrPrefix,
		AuthKeywords: l.AuthKeywords,
	}
}
