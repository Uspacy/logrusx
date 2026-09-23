package logrusx

import (
	"errors"
	"io"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

const defaultBufferSize = 100

// Type for passing logger requests over a goroutine
type logRequest struct {
	level  logrus.Level
	msg    string
	fields logrus.Fields
	done   chan struct{} // Closed after the request is written (used by Fatal)
}

type LogField struct {
	Key   string
	Value interface{}
}

type logger struct {
	logrusLogging *logrus.Logger
	fields        logrus.Fields   // Base fields, never mutated after New
	logChannel    chan logRequest // Channel for logger requests

	mu     sync.RWMutex  // Guards closed and sending to logChannel
	closed bool          // Set by Close, no more sends to logChannel after that
	done   chan struct{} // Closed when processLogQueue has drained the queue
}

type config struct {
	level  logrus.Level
	output io.Writer
}

// Option configures a logger created by New
type Option func(*config)

// WithLevel sets the minimum level of messages to be written (default: Info)
func WithLevel(level logrus.Level) Option {
	return func(c *config) { c.level = level }
}

// WithOutput sets the destination of log messages (default: os.Stdout)
func WithOutput(w io.Writer) Option {
	return func(c *config) { c.output = w }
}

// Create a new logger with JSON configuration and custom service name,
// returns error if service name is invalid.
// Close must be called before the program exits to flush queued messages.
func New(serviceName string, opts ...Option) (Logging, error) {
	if serviceName == "" {
		return nil, errors.New("invalid service name")
	}

	cfg := config{level: logrus.InfoLevel, output: os.Stdout}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	fieldMap := logrus.FieldMap{}
	fieldMap[logrus.FieldKeyMsg] = "message"

	Logger := logrus.New()
	Logger.SetOutput(cfg.output)
	Logger.SetLevel(cfg.level)
	Logger.SetFormatter(&logrus.JSONFormatter{
		PrettyPrint: false,
		FieldMap:    fieldMap,
	})

	// Create a channel for logs and run a goroutine for processing
	l := &logger{
		logrusLogging: Logger,
		fields:        logrus.Fields{"service": serviceName},
		logChannel:    make(chan logRequest, defaultBufferSize),
		done:          make(chan struct{}),
	}

	// Start goroutine to process logs
	go l.processLogQueue()

	return l, nil
}

type Logging interface {
	Info(msg string, fields ...LogField)
	Debug(msg string, fields ...LogField)
	Warning(msg string, fields ...LogField)
	Error(msg string, fields ...LogField)
	// Fatal blocks until the message is written, then exits the process
	Fatal(msg string, fields ...LogField)
	// Close flushes all queued messages and stops the background goroutine.
	// It is safe to call Close multiple times and concurrently with logging.
	// Messages logged after Close are written synchronously.
	Close() error
}

func (l *logger) Info(msg string, fields ...LogField) {
	l.log(logrus.InfoLevel, msg, fields)
}

func (l *logger) Debug(msg string, fields ...LogField) {
	l.log(logrus.DebugLevel, msg, fields)
}

func (l *logger) Warning(msg string, fields ...LogField) {
	l.log(logrus.WarnLevel, msg, fields)
}

func (l *logger) Error(msg string, fields ...LogField) {
	l.log(logrus.ErrorLevel, msg, fields)
}

func (l *logger) Fatal(msg string, fields ...LogField) {
	l.log(logrus.FatalLevel, msg, fields)
}

func (l *logger) Close() error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.logChannel)
	}
	l.mu.Unlock()

	<-l.done
	return nil
}

// Method that sends requests to the channel
func (l *logger) log(level logrus.Level, msg string, fields []LogField) {
	// Fatal must always exit, even if its level is disabled (same as logrus)
	if level != logrus.FatalLevel && !l.logrusLogging.IsLevelEnabled(level) {
		return
	}

	// Fields are captured at call time so later changes by the caller don't affect the entry
	req := logRequest{level: level, msg: msg, fields: l.fillFields(fields)}
	if level == logrus.FatalLevel {
		req.done = make(chan struct{})
	}

	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		// Wait for the queue tail to be written to preserve message order
		<-l.done
		l.write(req)
		return
	}
	l.logChannel <- req
	l.mu.RUnlock()

	if req.done != nil {
		<-req.done
	}
}

func (l *logger) processLogQueue() {
	defer close(l.done)
	for req := range l.logChannel {
		l.write(req)
	}
}

func (l *logger) write(req logRequest) {
	l.logrusLogging.WithFields(req.fields).Log(req.level, req.msg)
	if req.level == logrus.FatalLevel {
		l.logrusLogging.Exit(1)
	}
	if req.done != nil {
		close(req.done)
	}
}

func (l *logger) fillFields(fields []LogField) logrus.Fields {
	allFields := make(logrus.Fields, len(l.fields)+len(fields))

	for key, value := range l.fields {
		allFields[key] = value
	}

	for _, field := range fields {
		allFields[field.Key] = field.Value
	}

	return allFields
}
