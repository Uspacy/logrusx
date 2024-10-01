package logrusx

import (
	"errors"
	"os"

	"github.com/sirupsen/logrus"
)

// Type for passing logger requests over a goroutine
type logRequest struct {
	level  string
	msg    string
	fields []LogField
}

type LogField struct {
	Key   string
	Value interface{}
}

type logger struct {
	logrusLogging *logrus.Logger
	Fields        logrus.Fields
	logChannel    chan logRequest // Channel for logger requests
}

// Create a new logger with JSON configuration and custom service name,
// returns error if service name is invalid
func New(serviceName string) (Logging, error) {
	fieldMap := logrus.FieldMap{}
	fieldMap[logrus.FieldKeyMsg] = "message"

	Logger := logrus.New()
	Fields := logrus.Fields{}
	Logger.SetOutput(os.Stdout)
	Logger.SetFormatter(&logrus.JSONFormatter{
		PrettyPrint: false,
		FieldMap:    fieldMap,
	})
	if serviceName != "" {
		Fields["service"] = serviceName
	} else {
		return nil, errors.New("invalid service name")
	}

	// Create a channel for logs and run a goroutine for processing
	logChan := make(chan logRequest, 100) // Buffered channel for 100 requests
	l := &logger{
		logrusLogging: Logger,
		Fields:        Fields,
		logChannel:    logChan,
	}

	// Start goroutine to process logs
	go l.processLogQueue()

	return l, nil
}

type Logging interface {
	Info(msg string, fields ...LogField)
	Debug(msg string, fields ...LogField)
	Error(msg string, fields ...LogField)
	Fatal(msg string, fields ...LogField)
	fillFields(fields []LogField) logrus.Fields
}

func (l *logger) Info(msg string, fields ...LogField) {
	l.sendToChannel("info", msg, fields...)
}

func (l *logger) Debug(msg string, fields ...LogField) {
	l.sendToChannel("debug", msg, fields...)
}

func (l *logger) Error(msg string, fields ...LogField) {
	l.sendToChannel("error", msg, fields...)
}

func (l *logger) Fatal(msg string, fields ...LogField) {
	l.sendToChannel("fatal", msg, fields...)
}

// Method that sends requests to the channel
func (l *logger) sendToChannel(level string, msg string, fields ...LogField) {
	l.logChannel <- logRequest{
		level:  level,
		msg:    msg,
		fields: fields,
	}
}

func (l *logger) processLogQueue() {
	for req := range l.logChannel {
		allFields := l.fillFields(req.fields)

		switch req.level {
		case "info":
			l.logrusLogging.WithFields(allFields).Info(req.msg)
		case "debug":
			l.logrusLogging.WithFields(allFields).Debug(req.msg)
		case "error":
			l.logrusLogging.WithFields(allFields).Error(req.msg)
		case "fatal":
			l.logrusLogging.WithFields(allFields).Fatal(req.msg)
		}
	}
}

func (l *logger) fillFields(fields []LogField) logrus.Fields {
	allFields := make(logrus.Fields, len(l.Fields))

	for key, value := range l.Fields {
		allFields[key] = value
	}

	for _, field := range fields {
		allFields[field.Key] = field.Value
	}

	return allFields
}
