package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func testInput(id string, op string, val1 int, val2 int) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%d\n%d", id, op, val1, val2))
}

func Test_parser(t *testing.T) {
	type TestRun struct {
		name     string
		bytes    []byte
		expected Input
		err      error
	}
	data := make([]TestRun, 0, 6)
	data = append(data, TestRun{name: "sum", bytes: testInput("sum", "+", 1, 2), expected: Input{"sum", "+", 1, 2}, err: nil})
	data = append(data, TestRun{name: "sub", bytes: testInput("sub", "-", 3, 2), expected: Input{"sub", "-", 3, 2}, err: nil})
	data = append(data, TestRun{name: "mult", bytes: testInput("mult", "*", 2, 5), expected: Input{"mult", "*", 2, 5}, err: nil})
	data = append(data, TestRun{name: "div", bytes: testInput("div", "/", 4, 3), expected: Input{"div", "/", 4, 3}, err: nil})
	// error cases
	data = append(data, TestRun{name: "invalid val1", bytes: []byte(fmt.Sprintf("%s\n%s\n%s\n%d\n", "div", "/", "?", 1)), expected: Input{}, err: strconv.ErrSyntax})
	data = append(data, TestRun{name: "invalid val2", bytes: []byte(fmt.Sprintf("%s\n%s\n%d\n%s\n", "div", "/", 1, ".")), expected: Input{}, err: strconv.ErrSyntax})

	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			result, err := parser(d.bytes)
			if diff := cmp.Diff(result, d.expected); diff != "" {
				t.Error(diff)
			}

			if err != nil {
				if !errors.Is(err, strconv.ErrSyntax) {
					t.Error("unexpected error returned")
				}
			}
		})
	}
}

func Fuzz_parser(f *testing.F) {
	testcases := make([][]byte, 0, 6)
	testcases = append(testcases, testInput("sum", "+", 1, 2))
	testcases = append(testcases, testInput("sub", "-", 3, 2))
	testcases = append(testcases, testInput("mult", "*", 1, 2))
	testcases = append(testcases, testInput("div", "/", 1, 2))
	testcases = append(testcases, []byte(fmt.Sprintf("%s\n%s\n%s\n%d\n", "div", "/", "?", 1)))
	testcases = append(testcases, []byte(fmt.Sprintf("%s\n%s\n%d\n%s\n", "div", "/", 1, ".")))

	for _, tc := range testcases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := parser(in)
		if err != nil {
			if !errors.Is(err, strconv.ErrSyntax) && !errors.Is(err, MissingInputError{}) {
				t.Errorf("unexpected error returned: %v", err)
			}
		}
	})

}

func Test_DataProcessor(t *testing.T) {
	data := []struct {
		name     string
		inputs   [][]byte
		expected []Result
	}{
		{
			name: "valid operations",
			inputs: [][]byte{
				[]byte("sum\n+\n1\n2\n"),
				[]byte("sub\n-\n3\n2\n"),
				[]byte("mult\n*\n2\n5\n"),
				[]byte("div\n/\n4\n3\n"),
			},
			expected: []Result{
				{Id: "sum", Value: 3},
				{Id: "sub", Value: 1},
				{Id: "mult", Value: 10},
				{Id: "div", Value: 1},
			},
		},
		{
			name: "invalid operations",
			inputs: [][]byte{
				[]byte("invalid num1\n+\n?\n2\n"),
				[]byte("invalid num2\n+\n1\n?\n"),
				[]byte("invalid op\n!\n1\n2\n"),
				[]byte("div by 0\n/\n4\n0\n"),
			},
			expected: []Result{},
		},
	}

	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			in := make(chan []byte, len(d.inputs))
			out := make(chan Result, len(d.inputs))

			for _, input := range d.inputs {
				in <- input
			}

			close(in)

			go DataProcessor(in, out)

			var got []Result

			for res := range out {
				got = append(got, res)
			}

			if len(got) != len(d.expected) {
				t.Errorf("DataProcessor() got %v results, expected %v", len(got), len(d.expected))
			}

			for i, result := range got {
				if diff := cmp.Diff(result, d.expected[i]); diff != "" {
					t.Errorf("DataProcessor() result %v, expected %v", result, d.expected[i])
				}
			}
		})
	}
}

func Test_WriteData(t *testing.T) {
	data := []struct {
		name     string
		inputs   []Result
		expected []string
	}{
		{
			name: "correct format",
			inputs: []Result{
				{Id: "sum", Value: 3},
				{Id: "sub", Value: 1},
			},
			expected: []string{
				"sum:3\n",
				"sub:1\n",
			},
		},
	}

	var l sync.Mutex
	var wg sync.WaitGroup

	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			fmt.Println("in Write Data")
			in := make(chan Result)
			w := bytes.NewBuffer(nil)

			wg.Add(1)
			go func() {
				defer wg.Done()
				WriteData(in, w, &l)
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, input := range d.inputs {
					in <- input
				}
				close(in)
			}()

			wg.Wait()

			out := make([]byte, 6)
			count := 0
			for {
				_, err := w.Read(out)
				if err != nil {
					if count == len(d.expected) && err == io.EOF {
						fmt.Println("reached EOF of writer after all inputs were read in")
						break
					}

					t.Fatalf("unable to read data after call to WriteData(): %v", err)
				}

				s := string(out)
				if s != d.expected[count] {
					t.Errorf("WriteData result %v, expected %v", s, d.expected[count])
				}
				count++
			}
		})
	}

}

type BadRequestBodyStub struct{}

func (brbs BadRequestBodyStub) Read(p []byte) (n int, err error) {
	return 0, errors.New("test error")
}

func Test_NewController(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(5)

	out := make(chan []byte, 1)
	handler := NewController(out)

	data := []struct {
		name               string
		input              io.Reader
		expectedStatusCode int
		expectedBody       string
		channelBusy        bool
	}{
		{
			name:               "successful write to channel (1)",
			input:              strings.NewReader("sum\n+\n1\n2\n"),
			expectedStatusCode: http.StatusAccepted,
			expectedBody:       "OK: 1",
			channelBusy:        false,
		},
		{
			name:               "successful write to channel (2)",
			input:              strings.NewReader("sum\n+\n1\n2\n"),
			expectedStatusCode: http.StatusAccepted,
			expectedBody:       "OK: 2",
			channelBusy:        false,
		},
		{
			name:               "channel busy",
			input:              strings.NewReader("sum\n+\n1\n2\n"),
			expectedStatusCode: http.StatusServiceUnavailable,
			expectedBody:       "Too Busy: 1",
			channelBusy:        true,
		},
		{
			name:               "bad request",
			input:              BadRequestBodyStub{},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "Bad Input",
			channelBusy:        false,
		},
	}

	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			defer wg.Done()
			// fill the channel to simulate a busy state
			if d.channelBusy {
				fmt.Println("simulating busy channel")
				go func() {
					defer wg.Done()
					out <- []byte("sum\n+\n1\n2\n")
				}()
				fmt.Println("after channel send")
			} else {
				// ensure the channel is empty
				select {
				case <-out:
				default:
				}
			}

			req := httptest.NewRequest("POST", "/", d.input)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != d.expectedStatusCode {
				t.Errorf("NewController returned wrong status code: got %v want %v", rr.Code, d.expectedStatusCode)
			}

			if body := rr.Body.String(); body != d.expectedBody {
				t.Errorf("NewController returned unexpected body: got %v, want %v", body, d.expectedBody)
			}
		})
	}

	go func() {
		wg.Wait()
		close(out)
	}()
}
