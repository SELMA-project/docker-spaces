package main

// inspired by https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/

import (
	"fmt"
	_log "log"
	"os"
)

type LoggerLevel int

const (
	TraceLogLevel LoggerLevel = iota
	DebugLogLevel
	InfoLogLevel
	PrintLogLevel
	WarningLogLevel
	ErrorLogLevel
	FatalLogLevel
	PanicLogLevel
)

func (s LoggerLevel) String() string {
	return [...]string{"TRACE", "DEBUG", "INFO", "PRINT", "WARN", "ERROR", "FATAL", "PANIC"}[s]
}

type Logger interface {
	Log(...any)
	Logf(string, ...any)
}

type LevelCoreLogger interface {
	LLog(LoggerLevel, ...any)
	LLogf(LoggerLevel, string, ...any)
	SetLevel(LoggerLevel)
	GetLevel() LoggerLevel
	GetLogger() Logger
}

type LevelLogger interface {
	LLog(LoggerLevel, ...any)
	LLogf(LoggerLevel, string, ...any)
	SetLevel(LoggerLevel)
	GetLevel() LoggerLevel
	GetLogger() Logger
	Trace(...any)
	Tracef(string, ...any)
	Debug(...any)
	Debugf(string, ...any)
	Info(...any)
	Infof(string, ...any)
	Warn(...any)
	Warnf(string, ...any)
	Error(...any)
	Errorf(string, ...any)
	Fatal(...any)
	Fatalf(string, ...any)
	Panic(...any)
	Panicf(string, ...any)
}

type LevelLoggerCompatible interface {
	LLog(LoggerLevel, ...any)
	LLogf(LoggerLevel, string, ...any)
	SetLevel(LoggerLevel)
	GetLevel() LoggerLevel
	GetLogger() Logger
	Trace(...any)
	Tracef(string, ...any)
	Debug(...any)
	Debugf(string, ...any)
	Info(...any)
	Infof(string, ...any)
	Warn(...any)
	Warnf(string, ...any)
	Error(...any)
	Errorf(string, ...any)
	Fatal(...any)
	Fatalf(string, ...any)
	Panic(...any)
	Panicf(string, ...any)
	// log compatibility
	// Fatal(v ...any)
	// Fatalf(format string, v ...any)
	Fatalln(v ...any)
	// Panic(v ...any)
	// Panicf(format string, v ...any)
	Panicln(v ...any)
	Print(v ...any)
	Printf(format string, v ...any)
	Println(v ...any)
}

// standard logger wrapper implementing Logger interface

type LogWrapper struct {
	*_log.Logger
}

func (l *LogWrapper) Log(v ...any) {
	// TODO: handle file:line output
	l.Println(v...)
}

func (l *LogWrapper) Logf(format string, v ...any) {
	// TODO: handle file:line output
	l.Printf(format, v...)
}

// Logger wrapper implementing LevelCoreLogger interface

type LoggerLevelCoreWrapper struct {
	Logger
	// logger Logger
	level LoggerLevel
}

func NewLoggerLevelCoreWrapper(logger Logger) *LoggerLevelCoreWrapper {
	return &LoggerLevelCoreWrapper{logger, TraceLogLevel}
}

// func (l *LoggerLevelCoreWrapper) Logger() Logger {
// 	return l.logger
// }

func (l *LoggerLevelCoreWrapper) LLog(level LoggerLevel, v ...any) {
	if level >= l.level {
		if level != PrintLogLevel {
			v = append([]any{level.String()}, v...) // prepend
		}
		l.Log(v...)
		// l.logger.Log(v...)
		if level == FatalLogLevel {
			// TODO: fatal exit here
			os.Exit(1)
		} else if level == PanicLogLevel {
			// TODO: panic exit here
			panic("logger panic")
		}
	}
}

func (l *LoggerLevelCoreWrapper) LLogf(level LoggerLevel, format string, v ...any) {
	if level >= l.level {
		if level != PrintLogLevel {
			format = fmt.Sprintf("%s %s", level.String(), format)
		}
		l.Logf(format, v...)
		// l.logger.Logf(format, v...)
		if level == FatalLogLevel {
			// TODO: fatal exit here
			os.Exit(1)
		} else if level == PanicLogLevel {
			// TODO: panic exit here
			panic("logger panic")
		}
	}
}

func (l *LoggerLevelCoreWrapper) SetLevel(level LoggerLevel) {
	l.level = level
}

func (l *LoggerLevelCoreWrapper) GetLevel() LoggerLevel {
	return l.level
}

func (l *LoggerLevelCoreWrapper) GetLogger() Logger {
	return l.Logger
}

// per level Logger wrapper implementing LevelCoreLogger interface

type CoreMultiLevelLoggerWrapper struct {
	loggers map[LoggerLevel]Logger
	level   LoggerLevel
}

func (l *CoreMultiLevelLoggerWrapper) SetLevel(level LoggerLevel) {
	l.level = level
}

func (l *CoreMultiLevelLoggerWrapper) GetLevel() LoggerLevel {
	return l.level
}

func (l *CoreMultiLevelLoggerWrapper) SetLogger(level LoggerLevel, logger Logger) {
	if l.loggers == nil {
		l.loggers = make(map[LoggerLevel]Logger)
	}
	l.loggers[level] = logger
}

func (l *CoreMultiLevelLoggerWrapper) getLogger(level LoggerLevel) Logger {
	var logger Logger
	var loggerLevel LoggerLevel
	for lvl, lgr := range l.loggers {
		if lvl == level {
			logger = lgr
			break
		} else if lvl < level && (logger == nil || lvl > loggerLevel) {
			// next non empty lower level logger
			logger = lgr
			loggerLevel = lvl
		}
	}
	return logger
}

func (l *CoreMultiLevelLoggerWrapper) LLog(level LoggerLevel, v ...any) {
	if level >= l.level {
		logger := l.getLogger(level)
		logger.Log(v...)
	}
}

func (l *CoreMultiLevelLoggerWrapper) LLogf(level LoggerLevel, format string, v ...any) {
	if level >= l.level {
		logger := l.getLogger(level)
		logger.Logf(format, v...)
	}
}

// LevelCoreLogger wrapper implementing LevelLoger interface

type LoggerLevelWrapper struct {
	LevelCoreLogger
}

func (l *LoggerLevelWrapper) Trace(v ...any) {
	l.LLog(TraceLogLevel, v...)
}

func (l *LoggerLevelWrapper) Tracef(format string, v ...any) {
	l.LLogf(TraceLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Debug(v ...any) {
	l.LLog(DebugLogLevel, v...)
}

func (l *LoggerLevelWrapper) Debugf(format string, v ...any) {
	l.LLogf(DebugLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Info(v ...any) {
	l.LLog(InfoLogLevel, v...)
}

func (l *LoggerLevelWrapper) Infof(format string, v ...any) {
	l.LLogf(InfoLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Warn(v ...any) {
	l.LLog(WarningLogLevel, v...)
}

func (l *LoggerLevelWrapper) Warnf(format string, v ...any) {
	l.LLogf(WarningLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Error(v ...any) {
	l.LLog(ErrorLogLevel, v...)
}

func (l *LoggerLevelWrapper) Errorf(format string, v ...any) {
	l.LLogf(ErrorLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Fatal(v ...any) {
	l.LLog(FatalLogLevel, v...)
}

func (l *LoggerLevelWrapper) Fatalf(format string, v ...any) {
	l.LLogf(FatalLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Panic(v ...any) {
	l.LLog(PanicLogLevel, v...)
}

func (l *LoggerLevelWrapper) Panicf(format string, v ...any) {
	l.LLogf(PanicLogLevel, format, v...)
}

// compatilbity
func (l *LoggerLevelWrapper) Print(v ...any) {
	l.LLog(PrintLogLevel, v...)
}

func (l *LoggerLevelWrapper) Printf(format string, v ...any) {
	l.LLogf(PrintLogLevel, format, v...)
}

func (l *LoggerLevelWrapper) Println(v ...any) {
	l.LLog(PrintLogLevel, v...)
}

func (l *LoggerLevelWrapper) Fatalln(v ...any) {
	l.LLog(FatalLogLevel, v...)
}

func (l *LoggerLevelWrapper) Panicln(v ...any) {
	l.LLog(PanicLogLevel, v...)
}

func NewCompatibleDefaultLevelLogger() LevelLoggerCompatible {
	return &LoggerLevelWrapper{NewLoggerLevelCoreWrapper(&LogWrapper{_log.Default()})}
}

func NewCompatibleLevelLogger(logger *_log.Logger) LevelLoggerCompatible {
	return &LoggerLevelWrapper{NewLoggerLevelCoreWrapper(&LogWrapper{logger})}
}
