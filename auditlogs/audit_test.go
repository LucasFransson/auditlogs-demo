package auditlogs

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// MockLogFlusher is a mock implementation of the LogFlusher interface
type MockLogFlusher struct {
	mu           sync.Mutex
	flushedLogs  [][]LogEntry
	flushDelay   time.Duration
	shouldFail   bool
	flushCounter int
}

// Mock method to simulate flushing logs (with optional failure or delay for simulating / testing edge cases )
func (m *MockLogFlusher) flushLogs(logs []LogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.flushCounter++ // Increment the number of flush attempts

	if m.shouldFail {
		return // Simulate failure (by doing nothing)
	}

	if m.flushDelay > 0 {
		time.Sleep(m.flushDelay) // Simulate delay in flushing
	}

	logsCopy := make([]LogEntry, len(logs)) // Copy logs to prevent mutation
	copy(logsCopy, logs)
	m.flushedLogs = append(m.flushedLogs, logsCopy) // Append flushed logs
}

func (m *MockLogFlusher) getFlushedLogs() [][]LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flushedLogs
}

func (m *MockLogFlusher) getFlushCounter() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flushCounter
}

func TestBatchLogWorker(t *testing.T) {
	// Store the original logChannel (to reset after each test)
	originalLogChannel := logChannel

	// Helper function to reset logChannel after each test
	resetLogChannel := func() {
		logChannel = originalLogChannel
	}

	testLog := func(format string, args ...interface{}) {
		log.Printf("[TEST] "+format, args...)
	}

	// All these test cases should ofc be set up with table driven tests instead... BUT this is not a real use case or a real implementation, it's not even a real test. This is just for demoing the util of this approach in a test environment, so idc about rewriting thm!
	// TEST CASE SUMMARY
	// This test ensures that the batchLogWorker flushes the logs when the batch size is reached. It sends exactly the batch size (3 logs) to the log channel, then closes the channel and checks that the logs were flushed once with the correct number of logs (3)
	t.Run("Flush on full batch", func(t *testing.T) {
		defer resetLogChannel()

		testLog("Starting 'Flush on full batch' test")
		mockFlusher := &MockLogFlusher{}
		batchSize := 3
		flushInterval := 1 * time.Second

		logChannel = make(chan LogEntry, 10)
		done := make(chan bool)
		go func() {
			testLog("Starting batchLogWorker goroutine")
			batchLogWorker(mockFlusher, batchSize, flushInterval)
			done <- true
		}()

		testLog("Sending %d log entries", batchSize)
		for i := 0; i < batchSize; i++ {
			logChannel <- LogEntry{Timestamp: time.Now()}
		}

		testLog("Closing logChannel")
		close(logChannel)

		select {
		case <-done:
			testLog("batchLogWorker finished")
		case <-time.After(2 * time.Second):
			testLog("Test timed out")
			t.Fatal("Test timed out")
		}

		flushedLogs := mockFlusher.getFlushedLogs()
		testLog("Flushed logs: %d batches", len(flushedLogs))
		if len(flushedLogs) != 1 || len(flushedLogs[0]) != batchSize {
			t.Errorf("Expected 1 flush with %d logs, got %d flushes with %d logs", batchSize, len(flushedLogs), len(flushedLogs[0]))
		}
		testLog("'Flush on full batch' test completed")
	})

	// TEST CASE SUMMARY
	// This test checks whether the batchLogWorker flushes logs based on the time interval, even if the batch size is not reached
	// It sends fewer logs than the batch size, waits for the interval to pass, and verifies that the logs were flushed
	t.Run("Flush on interval", func(t *testing.T) {
		defer resetLogChannel()

		testLog("Starting 'Flush on interval' test")
		mockFlusher := &MockLogFlusher{}
		batchSize := 10
		flushInterval := 500 * time.Millisecond

		logChannel = make(chan LogEntry, 10)
		done := make(chan bool)
		go func() {
			testLog("Starting batchLogWorker goroutine")
			batchLogWorker(mockFlusher, batchSize, flushInterval)
			done <- true
		}()

		testLog("Sending 2 log entries")
		logChannel <- LogEntry{Timestamp: time.Now()}
		logChannel <- LogEntry{Timestamp: time.Now()}

		testLog("Waiting for flush interval (%v)", flushInterval)
		time.Sleep(flushInterval + 100*time.Millisecond)
		testLog("Closing logChannel")
		close(logChannel)

		select {
		case <-done:
			testLog("batchLogWorker finished")
		case <-time.After(2 * time.Second):
			testLog("Test timed out")
			t.Fatal("Test timed out")
		}

		flushedLogs := mockFlusher.getFlushedLogs()
		testLog("Flushed logs: %d batches", len(flushedLogs))
		if len(flushedLogs) != 1 || len(flushedLogs[0]) != 2 {
			t.Errorf("Expected 1 flush with 2 logs, got %d flushes with %d logs", len(flushedLogs), len(flushedLogs[0]))
		}
		testLog("'Flush on interval' test completed")
	})

	// TEST CASE SUMMARY
	// This test simulates a slow log flusher by introducing a delay in the flushLogs method
	// It sends multiple logs in batches and verifies that all logs are eventually flushed, despite the delay
	t.Run("Handle slow flusher", func(t *testing.T) {
		defer resetLogChannel()

		testLog("Starting 'Handle slow flusher' test")
		mockFlusher := &MockLogFlusher{flushDelay: 200 * time.Millisecond}
		batchSize := 3
		flushInterval := 1 * time.Second

		logChannel = make(chan LogEntry, 10)
		done := make(chan bool)
		go func() {
			testLog("Starting batchLogWorker goroutine")
			batchLogWorker(mockFlusher, batchSize, flushInterval)
			done <- true
		}()

		testLog("Sending 9 log entries (3 batches)")
		for i := 0; i < 9; i++ {
			logChannel <- LogEntry{Timestamp: time.Now()}
		}

		testLog("Closing logChannel")
		close(logChannel)

		select {
		case <-done:
			testLog("batchLogWorker finished")
		case <-time.After(3 * time.Second):
			testLog("Test timed out")
			t.Fatal("Test timed out")
		}

		flushedLogs := mockFlusher.getFlushedLogs()
		testLog("Flushed logs: %d batches", len(flushedLogs))
		if len(flushedLogs) != 3 {
			t.Errorf("Expected 3 flushes, got %d", len(flushedLogs))
		}
		testLog("'Handle slow flusher' test completed")
	})

	// TEST CASE SUMMARY
	// This test simulates a flusher failure and checks that the correct number of flush attempts are made, but no logs are successfully flushed
	t.Run("Handle flusher failure", func(t *testing.T) {
		defer resetLogChannel()

		testLog("Starting 'Handle flusher failure' test")
		mockFlusher := &MockLogFlusher{shouldFail: true} // Simulate flusher failure
		batchSize := 3
		flushInterval := 1 * time.Second

		logChannel = make(chan LogEntry, 10) // Set log channel buffer size
		done := make(chan bool)
		go func() {
			testLog("Starting batchLogWorker goroutine")
			batchLogWorker(mockFlusher, batchSize, flushInterval)
			done <- true
		}()

		testLog("Sending 6 log entries (2 batches)")
		for i := 0; i < 6; i++ {
			logChannel <- LogEntry{Timestamp: time.Now()} // Send logs
		}

		testLog("Closing logChannel")
		close(logChannel)

		// Wait for the worker to finish
		select {
		case <-done:
			testLog("batchLogWorker finished")
		case <-time.After(2 * time.Second):
			testLog("Test timed out")
			t.Fatal("Test timed out")
		}

		// Verify that 2 flush attempts were made but no logs were successfully flushed
		flushCounter := mockFlusher.getFlushCounter()
		testLog("Flush attempts: %d", flushCounter)
		if flushCounter != 2 {
			t.Errorf("Expected 2 flush attempts, got %d", flushCounter)
		}

		flushedLogs := mockFlusher.getFlushedLogs()
		testLog("Successfully flushed logs: %d", len(flushedLogs))
		if len(flushedLogs) != 0 {
			t.Errorf("Expected 0 successful flushes, got %d", len(flushedLogs))
		}
		testLog("'Handle flusher failure' test completed")
	})
}

// TODO: This test fails randomly (if executed multiple times in a short period); Might be our test setup, but could also be pointing to real timing issues/race conditions in the logging system. Worth a closer look at both the test and the actual code
// TODO: Debug notes: The batchLogWorker runs in a separate goroutine, and there might be a race condition between when the log is sent and when it's flushed; The test waits for 50 milliseconds after sending the request before checking for flushed logs, => might not always be enough time for the log to be processed and flushed
// TestInitAuditLogger checks that logChannel and fallbackFile are initialized correctly during the logger setup
func TestInitAuditLogger(t *testing.T) {
	t.Log("Starting TestInitAuditLogger")

	// Temporary file for testing
	tmpfile, err := ioutil.TempFile("", "fallback_logs_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	t.Logf("Created temporary fallback file: %s", tmpfile.Name())

	oldFallbackFilePath := fallbackFilePath
	fallbackFilePath = tmpfile.Name() // Set test fallback file path
	defer func() { fallbackFilePath = oldFallbackFilePath }()

	mockFlusher := &MockLogFlusher{}
	t.Log("Initializing audit logger")
	InitAuditLogger(mockFlusher, 100, 2, 10, time.Second)

	// Check if logChannel was initialized
	if logChannel == nil {
		t.Error("logChannel was not initialized")
	} else {
		t.Log("logChannel was successfully initialized")
	}

	// Check if fallbackFile was initialized
	if fallbackFile == nil {
		t.Error("fallbackFile was not initialized")
	} else {
		t.Log("fallbackFile was successfully initialized")
	}

	// Clean up
	t.Log("Closing fallback file")
	fallbackFile.Close()

	t.Log("TestInitAuditLogger completed")
}

func TestAuditLoggingMiddleware(t *testing.T) {
	t.Log("Starting TestAuditLoggingMiddleware")

	mockFlusher := &MockLogFlusher{}
	t.Log("Initializing audit logger")
	InitAuditLogger(mockFlusher, 100, 1, 10, 10*time.Millisecond)

	handler := AuditLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Log("Sending test request")
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	} else {
		t.Log("Handler returned correct status code")
	}

	t.Log("Waiting for log to be processed")
	time.Sleep(50 * time.Millisecond)

	flushedLogs := mockFlusher.getFlushedLogs()
	if len(flushedLogs) == 0 || len(flushedLogs[0]) == 0 {
		t.Errorf("Expected at least 1 flushed log, got %d", len(flushedLogs))
	} else {
		t.Logf("Received %d flushed logs", len(flushedLogs))
	}

	t.Log("Testing fallback logging")
	oldLogChannel := logChannel
	logChannel = make(chan LogEntry, 1) // This will be full after one log
	defer func() { logChannel = oldLogChannel }()

	// Fill the channel
	logChannel <- LogEntry{}

	req = httptest.NewRequest("GET", "/test-fallback", nil)
	rr = httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	t.Log("Waiting for fallback log to be written")
	time.Sleep(50 * time.Millisecond)

	// Check if fallback log was written
	content, err := ioutil.ReadFile(fallbackFilePath)
	if err != nil {
		t.Fatalf("Failed to read fallback file: %v", err)
	}
	if len(content) == 0 {
		t.Error("Fallback log was not written")
	} else {
		t.Log("Fallback log was successfully written")
	}

	t.Log("TestAuditLoggingMiddleware completed")
}

// TestFallbackLog validates that the fallbackLog function correctly writes log entries to a file
func TestFallbackLog(t *testing.T) {
	t.Log("Starting TestFallbackLog")

	// Temporary file for testing
	tmpfile, err := ioutil.TempFile("", "fallback_logs_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	t.Logf("Created temporary fallback file: %s", tmpfile.Name())

	oldFallbackFile := fallbackFile
	fallbackFile = tmpfile // Set test fallback file
	defer func() { fallbackFile = oldFallbackFile }()

	// Test log entry
	testEntry := LogEntry{
		Timestamp: time.Now(),
		Method:    "GET",
		URL:       "/test",
		Status:    200,
		UserAgent: "TestAgent",
		IPAddress: "127.0.0.1",
	}

	t.Log("Writing test log entry to fallback file")
	err = fallbackLog(testEntry)
	if err != nil {
		t.Fatalf("fallbackLog failed: %v", err)
	}

	t.Log("Reading fallback file content")
	content, err := ioutil.ReadFile(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to read fallback file: %v", err)
	}

	var loggedEntry LogEntry
	t.Log("Unmarshaling logged entry")
	err = json.Unmarshal(content, &loggedEntry)
	if err != nil {
		t.Fatalf("Failed to unmarshal logged entry: %v", err)
	}

	// Verify that the logged entry matches the original test entry
	if loggedEntry.Method != testEntry.Method || loggedEntry.URL != testEntry.URL {
		t.Errorf("Logged entry does not match test entry")
	} else {
		t.Log("Logged entry matches test entry")
	}

	t.Log("TestFallbackLog completed")
}

// TestLoggingResponseWriter validates that the loggingResponseWriter correctly captures and records HTTP response status codes
func TestLoggingResponseWriter(t *testing.T) {
	t.Log("Starting TestLoggingResponseWriter")

	rec := httptest.NewRecorder()
	lrw := &loggingResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	t.Log("Setting custom status code")
	lrw.WriteHeader(http.StatusNotFound) // Set custom status code

	// Verify that the loggingResponseWriter captured the correct status code
	if lrw.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code %d, got %d", http.StatusNotFound, lrw.statusCode)
	} else {
		t.Log("loggingResponseWriter captured correct status code")
	}

	if rec.Code != http.StatusNotFound {
		t.Errorf("Expected recorder status code %d, got %d", http.StatusNotFound, rec.Code)
	} else {
		t.Log("Underlying ResponseRecorder has correct status code")
	}

	t.Log("TestLoggingResponseWriter completed")
}

// TestLokiFlusher validates that logs are successfully sent to a Loki instance
// and that error handling works correctly for various scenarios.
func TestLokiFlusher(t *testing.T) {
	logs := []LogEntry{
		{Timestamp: time.Now(), Method: "GET", URL: "/test", Status: 200},
	}

	tests := []struct {
		name           string
		serverResponse func(http.ResponseWriter, *http.Request)
		lokiURL        string
		expectedOutput string
		validateFunc   func(*testing.T, *httptest.Server, string)
	}{
		{
			// TEST CASE SUMMARY
			// This test verifies that logs are successfully sent to Loki with the correct request format (checks the HTTP method, URL path, content type, and payload structure of the reques)
			name: "Successful log flush",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				t.Log("Validating request details")
				if r.Method != "POST" {
					t.Errorf("Expected POST request, got %s", r.Method)
				}
				if r.URL.Path != "/loki/api/v1/push" {
					t.Errorf("Expected /loki/api/v1/push path, got %s", r.URL.Path)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
				}

				t.Log("Validating request body")
				body, _ := ioutil.ReadAll(r.Body)
				var payload LokiPayload
				json.Unmarshal(body, &payload)
				if len(payload.Streams) != 1 || len(payload.Streams[0].Values) != 1 {
					t.Errorf("Unexpected payload structure: %+v", payload)
				}

				w.WriteHeader(http.StatusNoContent)
			},
			expectedOutput: "",
			validateFunc: func(t *testing.T, server *httptest.Server, output string) {
				if output != "" {
					t.Errorf("Expected no output for successful case, got: %s", output)
				}
			},
		},
		{
			// TEST CASE SUMMARY
			// This test simulates a server error response from Loki (500 Internal Server Error) (checks that the LokiFlusher correctly handles and reports this error)
			name: "Server error response",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				t.Log("Simulating server error response")
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedOutput: "Unexpected response from Loki: 500 Internal Server Error",
			validateFunc: func(t *testing.T, server *httptest.Server, output string) {
				if !strings.Contains(output, "Unexpected response from Loki: 500 Internal Server Error") {
					t.Errorf("Expected error message about 500 Internal Server Error, got: %s", output)
				}
			},
		},
		{
			// TEST CASE SUMMARY
			// This test checks the error handling when an invalid Loki URL is provided (verifies that the LokiFlusher correctly reports the unsupported protocol scheme error)
			name:           "Invalid URL",
			lokiURL:        "invalid-url",
			expectedOutput: "Failed to send logs to Loki: Post \"invalid-url/loki/api/v1/push\": unsupported protocol scheme \"\"",
			validateFunc: func(t *testing.T, server *httptest.Server, output string) {
				if !strings.Contains(output, "Failed to send logs to Loki: Post \"invalid-url/loki/api/v1/push\": unsupported protocol scheme \"\"") {
					t.Errorf("Expected error message about unsupported protocol scheme, got: %s", output)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Starting test case: %s", tt.name)

			var server *httptest.Server
			if tt.serverResponse != nil {
				server = httptest.NewServer(http.HandlerFunc(tt.serverResponse))
				defer server.Close()
				t.Logf("Test server started at %s", server.URL)
			}

			flusherURL := tt.lokiURL
			if flusherURL == "" && server != nil {
				flusherURL = server.URL
			}
			t.Logf("Using Loki URL: %s", flusherURL)

			flusher := NewLokiFlusher(flusherURL, "test-tenant")
			t.Log("LokiFlusher created, beginning log flush")

			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			flusher.flushLogs(logs)

			// Restore stdout
			w.Close()
			os.Stdout = oldStdout
			captured, _ := ioutil.ReadAll(r)

			t.Logf("Captured output: %s", string(captured))
			t.Log("Validating results")
			tt.validateFunc(t, server, string(captured))
			t.Log("Test case completed")
		})
	}
}
