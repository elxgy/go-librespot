package go_librespot

type Logger interface {
	Tracef(format string, args ...any)
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)

	Trace(args ...any)
	Debug(args ...any)
	Info(args ...any)
	Warn(args ...any)
	Error(args ...any)

	WithField(key string, value any) Logger
	WithError(err error) Logger
}

type NullLogger struct{}

func (l *NullLogger) Tracef(string, ...any) {}
func (l *NullLogger) Debugf(string, ...any) {}
func (l *NullLogger) Infof(string, ...any)  {}
func (l *NullLogger) Warnf(string, ...any)  {}
func (l *NullLogger) Errorf(string, ...any) {}

func (l *NullLogger) Trace(...any) {}
func (l *NullLogger) Debug(...any) {}
func (l *NullLogger) Info(...any)  {}
func (l *NullLogger) Warn(...any)  {}
func (l *NullLogger) Error(...any) {}

func (l *NullLogger) WithField(string, any) Logger { return l }
func (l *NullLogger) WithError(error) Logger       { return l }
