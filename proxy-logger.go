package main

import "fmt"

type ProxyBaseLogger LevelLoggerCompatible

type ProxyLoggerFlowPrefix struct {
	Source   string
	Target   string
	Backward bool
}

func (c *ProxyLoggerFlowPrefix) String() string {
	var s string
	if len(c.Source) > 0 {
		s = c.Source
		if len(c.Target) > 0 {
			var transfer string
			if c.Backward {
				transfer = " <-- "
			} else {
				transfer = " --> "
			}
			s = s + transfer + c.Target // source --> target | source <-- target
		}
		s = "[" + s + "]" // wrap inside []
	}
	// if len(c.Prefix) > 0 {
	// 	s = s + " " + c.Prefix
	// }
	return s
}

type errorFmt struct {
	*ProxyLogger
}

type ProxyLogger struct {
	ProxyBaseLogger
	flowPrefix   ProxyLoggerFlowPrefix
	parentPrefix string
	prefix       string
	_fullPrefix  string
	_prefixBuilt bool
	E            *errorFmt
}

func NewProxyLogger(logger ProxyBaseLogger) *ProxyLogger {
	return (&ProxyLogger{logger, ProxyLoggerFlowPrefix{}, "", "", "", false, &errorFmt{}}).SetLevel(logger.GetLevel())._initErrorFmt()
}

// func NewProxyLogger(logger ProxyBaseLogger, flowPrefix ProxyLoggerFlowPrefix, prefix string, level LoggerLevel) *ProxyLogger {
// 	return (&ProxyLogger{logger, flowPrefix, "", prefix, ""}).Build().SetLevel(logger.GetLevel())
// }

// func (l *ProxyLogger) NewLogger() *ProxyLogger {
// 	return (&ProxyLogger{l.ProxyBaseLogger, l.flowPrefix, l.prefix, "", ""})
// }

func (l *ProxyLogger) Derive() *ProxyLogger {
	// create new logger with the same level and base logger
	// TODO: improve this
	log := &LoggerLevelWrapper{NewLoggerLevelCoreWrapper(l.ProxyBaseLogger.GetLogger())}
	log.SetLevel(l.GetLevel())
	return (&ProxyLogger{log, l.flowPrefix, l.prefix, "", "", false, &errorFmt{}})._initErrorFmt()
}

func (l *ProxyLogger) _initErrorFmt() *ProxyLogger {
	l.E.ProxyLogger = l
	return l
}

func (l *ProxyLogger) WithExtension(prefixExtension string) *ProxyLogger {
	return (&ProxyLogger{l.ProxyBaseLogger, l.flowPrefix, l.parentPrefix, l.prefix + prefixExtension, "", false, &errorFmt{}})._initErrorFmt()
}

func (l *ProxyLogger) SetLevel(level LoggerLevel) *ProxyLogger {
	l.ProxyBaseLogger.SetLevel(level)
	return l
}

func (l *ProxyLogger) SetPrefix(prefix string) *ProxyLogger {
	l.prefix = prefix
	l._prefixBuilt = false
	return l
}

func (l *ProxyLogger) SetSource(source string) *ProxyLogger {
	l.flowPrefix.Source = source
	l.flowPrefix.Target = ""
	l._prefixBuilt = false
	return l
}

func (l *ProxyLogger) SetTarget(target string, backward bool) *ProxyLogger {
	l.flowPrefix.Target = target
	l.flowPrefix.Backward = backward
	l._prefixBuilt = false
	return l
}

func (l *ProxyLogger) Build() *ProxyLogger {
	p := l.flowPrefix.String()
	if len(l.parentPrefix) > 0 {
		if len(p) > 0 {
			p = p + " "
		}
		p = p + l.parentPrefix + ":"
	}
	if len(l.prefix) > 0 {
		if len(p) > 0 {
			p = p + " "
		}
		p = p + l.prefix + ":"
	}
	l._fullPrefix = p
	return l
}

func (l *ProxyLogger) fullPrefix() string {
	if !l._prefixBuilt {
		p := l.flowPrefix.String()
		if len(l.parentPrefix) > 0 {
			if len(p) > 0 {
				p = p + " "
			}
			p = p + l.parentPrefix + ":"
		}
		if len(l.prefix) > 0 {
			if len(p) > 0 {
				p = p + " "
			}
			p = p + l.prefix + ":"
		}
		l._fullPrefix = p
		l._prefixBuilt = true
	}
	return l._fullPrefix
}

func (l *ProxyLogger) Prefix() string {
	return l.prefix
}

func (l *ProxyLogger) Trace(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Trace(v...)
}

func (l *ProxyLogger) Tracef(format string, v ...any) {
	l.ProxyBaseLogger.Tracef(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Debug(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Debug(v...)
}

func (l *ProxyLogger) Debugf(format string, v ...any) {
	l.ProxyBaseLogger.Debugf(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Info(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Info(v...)
}

func (l *ProxyLogger) Infof(format string, v ...any) {
	l.ProxyBaseLogger.Infof(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Warn(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Warn(v...)
}

func (l *ProxyLogger) Warnf(format string, v ...any) {
	l.ProxyBaseLogger.Warnf(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Error(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Error(v...)
}

func (l *ProxyLogger) Errorf(format string, v ...any) {
	l.ProxyBaseLogger.Errorf(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Fatal(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Fatal(v...)
}

func (l *ProxyLogger) Fatalf(format string, v ...any) {
	l.ProxyBaseLogger.Fatalf(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Panic(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Panic(v...)
}

func (l *ProxyLogger) Panicf(format string, v ...any) {
	l.ProxyBaseLogger.Panicf(l.fullPrefix()+" "+format, v...)
}

// compatilbity
func (l *ProxyLogger) Print(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Print(v...)
}

func (l *ProxyLogger) Printf(format string, v ...any) {
	l.ProxyBaseLogger.Printf(l.fullPrefix()+" "+format, v...)
}

func (l *ProxyLogger) Println(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Println(v...)
}

func (l *ProxyLogger) Fatalln(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Fatalln(v...)
}

func (l *ProxyLogger) Panicln(v ...any) {
	v = append([]any{l.fullPrefix()}, v...)
	l.ProxyBaseLogger.Panicln(v...)
}

func (e *errorFmt) Errorf(format string, v ...any) error {
	return fmt.Errorf(e.ProxyLogger.Prefix()+": "+format, v...)
}

// passthrough
func (e *errorFmt) Sprintf(format string, v ...any) string {
	return fmt.Sprintf(format, v...)
}
