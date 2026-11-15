package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	levelDebug = 10
	levelInfo  = 20
	levelWarn  = 30
	levelError = 40
)

var levelNames = map[string]int{
	"debug": levelDebug,
	"info":  levelInfo,
	"warn":  levelWarn,
	"error": levelError,
}

// Logger escribe a consola y, opcionalmente, a un archivo con rotación simple.
type Logger struct {
	mu        sync.Mutex
	threshold int
	timeZone  *time.Location
	file      *os.File
	filePath  string
}

// New crea un logger. level es debug, info, warn o error. timeZone puede ser
// una zona IANA (America/Argentina/Buenos_Aires) o vacía para la local.
func New(level, timeZone, logFile string) *Logger {
	threshold, ok := levelNames[strings.ToLower(level)]
	if !ok {
		threshold = levelInfo
	}

	loc := time.Local
	if strings.TrimSpace(timeZone) != "" {
		if parsed, err := time.LoadLocation(strings.TrimSpace(timeZone)); err == nil {
			loc = parsed
		}
	}

	l := &Logger{threshold: threshold, timeZone: loc}
	if logFile != "" {
		l.openFile(logFile)
	}
	return l
}

func (l *Logger) openFile(path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 5*1024*1024 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	l.file = file
	l.filePath = path
}

func (l *Logger) Debugf(format string, args ...any) { l.write(levelDebug, "DEBUG", format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.write(levelInfo, "INFO", format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.write(levelWarn, "WARN", format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.write(levelError, "ERROR", format, args...) }

func (l *Logger) write(level int, name, format string, args ...any) {
	if l == nil || level < l.threshold {
		return
	}
	message := fmt.Sprintf(format, args...)
	now := time.Now()
	stamp := now.In(l.timeZone).Format("2006-01-02 15:04:05")

	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(os.Stdout, "[%s] %-5s %s\n", stamp, name, message)
	if l.file != nil {
		fmt.Fprintf(l.file, "%s [%s] %s\n", now.UTC().Format(time.RFC3339), name, message)
	}
}

func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
