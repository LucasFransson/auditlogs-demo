package auditlogs

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// LogEntry represents a single audit log entry
type LogEntry struct {
	Timestamp time.Time
	Method    string
	URL       string
	Status    int
	UserAgent string
	IPAddress string
}

var (
	logChannel       chan LogEntry
	fallbackFile     *os.File
	fallbackFilePath = "fallback_logs.json"
)

// FallbackHandler defines the interface for handling fallback log storage
type FallbackHandler interface {
	Handle(logs []LogEntry) error
}

type FileFallbackHandler struct {
	FilePath string
}

func (fh *FileFallbackHandler) Handle(logs []LogEntry) error {
	file, err := os.OpenFile(fh.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open fallback file: %w", err)
	}
	defer file.Close()

	for _, log := range logs {
		data, err := json.Marshal(log)
		if err != nil {
			return fmt.Errorf("failed to marshal log entry: %w", err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("failed to write log entry to file: %w", err)
		}
	}
	return nil
}

// LogFlusher is an interface for handling the flushing of audit logs
type LogFlusher interface {
	flushLogs([]LogEntry)
}

// batchLogWorker is a goroutine that collects log entries and flushes them in batches
// It also processes/ensures logs are flushed at regular intervals even if the batch size isn't reached (flushinterval)
func batchLogWorker(logFlusher LogFlusher, batchSize int, flushInterval time.Duration) {
	var logs []LogEntry
	timer := time.NewTimer(flushInterval)

	for {
		select {
		case log, ok := <-logChannel: // Receive log entry  from the channel
			if !ok {
				// Channel is closed, flush remaining logs
				if len(logs) > 0 {
					logFlusher.flushLogs(logs)
				}
				return
			}
			logs = append(logs, log)    // Add log entry to the batch
			if len(logs) >= batchSize { // If batch size is reached, flush the logs
				logFlusher.flushLogs(logs)
				logs = nil // Clear the batch after flushing
			}
		case <-timer.C: // Periodic flush based on time (flushinterval)
			if len(logs) > 0 {
				logFlusher.flushLogs(logs)
				logs = nil // Clear the batch after flushing
			}
			timer.Reset(flushInterval) // Reset the timer for the next interval
		}
	}
}

// SQL POSTGRES FLUSHER (SUPER SIMPLIFIED IMPLEMENTATION TO SHOWCASE THE INTERFACE AND CODE STRUCTURE)
// sqlLogFlusher implements LogFlusher for SQL databases
type SqlLogFlusher struct {
	sql.DB          // Should also be an interface, but this is just a proof of concept demo for the audit logs functionallity
	FallbackHandler FallbackHandler
}

// flushLogs writes a batch of log entries to the SQL database; (uses transactions and retries on failure)
func (sf *SqlLogFlusher) flushLogs(logs []LogEntry) {
	if len(logs) == 0 {
		return // Exit early, If there are no logs to flush
	}

	// Retry up to 3 times if there is a failure
	for retries := 0; retries < 3; retries++ { // Yes, this should ofc not be a hardcoded magic number defined inside here, in a real code solution this should be "lifted out" & passed to the struct implementing the LogFlusher interface,.
		tx, err := sf.DB.Begin() // Begin a new transaction
		if err != nil {
			fmt.Printf("Failed to begin transaction (retry %d): %v\n", retries, err)
			time.Sleep(time.Second * time.Duration(retries))
			continue
		}

		stmt, err := tx.Prepare("INSERT INTO audit_logs (timestamp, method, url, status, user_agent, ip_address) VALUES ($1, $2, $3, $4, $5, $6)")
		if err != nil {
			fmt.Printf("Failed to prepare statement (retry %d): %v\n", retries, err)
			tx.Rollback() // Rollback the transaction if there is a failure
			time.Sleep(time.Second * time.Duration(retries))
			continue
		}

		success := true // Track success across all log entries
		for _, log := range logs {
			_, err := stmt.Exec(log.Timestamp, log.Method, log.URL, log.Status, log.UserAgent, log.IPAddress)
			if err != nil {
				fmt.Printf("Failed to execute statement (retry %d): %v\n", retries, err)
				success = false
				break // Break on failure to retry the whole batch
			}
		}
		stmt.Close()

		// Commit the transaction if all log entries were successfully written
		if success {
			err = tx.Commit()
			if err != nil {
				fmt.Printf("Failed to commit transaction (retry %d): %v\n", retries, err)
				time.Sleep(time.Second * time.Duration(retries))
				continue
			}
			return // Exit after a successful flush
		} else {
			tx.Rollback() // Rollback transaction on failure
		}
	}
	// 3 failed writing to db attempts, Invoke the fallback
	fmt.Println("Database save failed. Falling back to alternative storage.")
	if sf.FallbackHandler != nil {
		if err := sf.FallbackHandler.Handle(logs); err != nil {
			log.Printf("Fallback storage also failed: %v\n", err)
		}
	}
}

// LOKI FLUSHER (MADE UP USE CASE/IMPLEMENTATION TO SHOWCASE THE INTERFACE AND CODE STRUCTURE)
// LokiFlusher implements LogFlusher for sending logs to Loki
type LokiFlusher struct {
	URL    string
	Tenant string
	Client *http.Client
}

// NewLokiFlusher creates a new LokiFlusher instance
func NewLokiFlusher(url, tenant string) *LokiFlusher {
	return &LokiFlusher{
		URL:    url,
		Tenant: tenant,
		Client: &http.Client{
			Timeout: 10 * time.Second, // timeout for HTTP requests
		},
	}
}

// flushLogs sends a batch of log entries to Loki
func (lf *LokiFlusher) flushLogs(logs []LogEntry) {
	if len(logs) == 0 {
		return // Exit early if no logs to flush
	}

	// Construct the Loki stream and values to send the logs
	streams := []LokiStream{
		{
			Stream: map[string]string{
				"job": "audit_logs",
			},
			Values: make([][]string, len(logs)),
		},
	}

	// Marshal each log entry to JSON and add it to the Loki stream
	for i, log := range logs {
		logLine, err := json.Marshal(log)
		if err != nil {
			fmt.Printf("Failed to marshal log entry: %v\n", err)
			continue
		}
		streams[0].Values[i] = []string{
			fmt.Sprintf("%d", log.Timestamp.UnixNano()),
			string(logLine),
		}
	}

	// Build the payload to send to Loki
	payload := LokiPayload{
		Streams: streams,
	}

	// Serialize the payload to JSON
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Failed to marshal Loki payload: %v\n", err)
		return
	}

	// Create the HTTP request to send logs to Loki
	req, err := http.NewRequest("POST", lf.URL+"/loki/api/v1/push", bytes.NewBuffer(jsonPayload))
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if lf.Tenant != "" {
		req.Header.Set("X-Scope-OrgID", lf.Tenant)
	}

	// Send the request and handle the response
	resp, err := lf.Client.Do(req)
	if err != nil {
		fmt.Printf("Failed to send logs to Loki: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		fmt.Printf("Unexpected response from Loki: %s\n", resp.Status)
	}
}

// LokiStream represents a stream of log entries in Loki
type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

// LokiPayload represents the payload sent to Loki's push API
type LokiPayload struct {
	Streams []LokiStream `json:"streams"`
}

// AUDIT LOGGER SETUP AND MIDDLEWARE CODE

// InitAuditLogger initializes the audit logging system
// It sets up the log channel, opens the fallback file, and starts worker goroutines
// The workers will listen to the log channel and process entries in batches
func InitAuditLogger(logFlusher LogFlusher, bufferSize int, numWorkers int, batchSize int, flushInterval time.Duration) {
	logChannel = make(chan LogEntry, bufferSize)
	var err error
	fallbackFile, err = os.OpenFile(fallbackFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open fallback file: %v", err)
	}

	// Spawn worker goroutines for processing logs in parallel
	for i := 0; i < numWorkers; i++ { // Launch multiple goroutines based on numWorkers
		go batchLogWorker(logFlusher, batchSize, flushInterval) // Each goroutine runs the batchLogWorker function
	}
}

// AuditLoggingMiddleware is an HTTP middleware that logs audit information for each request
// it intercepts each HTTP request, records audit details, creates a logEntry and pushes it to the log channel
func AuditLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now() // Record the request start time
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r) // Call the next handler in the middleware chain

		logEntry := LogEntry{
			Timestamp: start,
			Method:    r.Method,
			URL:       r.URL.String(),
			Status:    lrw.statusCode,
			UserAgent: r.UserAgent(),
			IPAddress: r.RemoteAddr,
		}

		// Attempt to send the log entry to the log channel
		select {
		case logChannel <- logEntry:
		default:
			// Channel is full, use fallback logging
			err := fallbackLog(logEntry)
			if err != nil {
				log.Printf("Failed to write log entry to fallback file: %v\n", err)
				log.Printf("Dropping log entry due to full channel: %v\n", logEntry)
			}
		}
	})
}

// fallbackLog writes a log entry to the fallback file when the log channel is full
// This ensures that logs are not completely lost even if the system is under heavy load & the log channel is full
func fallbackLog(entry LogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %v", err)
	}
	_, err = fallbackFile.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("failed to write log entry to file: %v", err)
	}
	return nil
}

// loggingResponseWriter is a wrapper for http.ResponseWriter that captures the status code
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before writing it to the underlying ResponseWriter
func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}
