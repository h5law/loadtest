/*
Copyright © 2023 h5law <hrryslw@pm.me>
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

 1. Redistributions of source code must retain the above copyright notice,
    this list of conditions and the following disclaimer.

 2. Redistributions in binary form must reproduce the above copyright notice,
    this list of conditions and the following disclaimer in the documentation
    and/or other materials provided with the distribution.

 3. Neither the name of the copyright holder nor the names of its contributors
    may be used to endorse or promote products derived from this software
    without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.
*/
package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// TODO: make this configurable
const (
	queryPath = "/v1/query/height"
)

var (
	outputFilePath string
	batchSize      int
	totalRequests  int
	WarningLogger  *log.Logger
	InfoLogger     *log.Logger
	ErrorLogger    *log.Logger
)

func init() {
	queryCmd := newQueryCommand()

	queryCmd.Flags().StringVarP(&outputFilePath, "output-file", "o", "query-results.json", "query data output file path")
	queryCmd.Flags().IntVarP(&batchSize, "batch-size", "b", 100, "number of requests to send in each batch")
	queryCmd.Flags().IntVarP(&totalRequests, "total-requests", "t", 10000, "number of requests to send in each batch")

	rootCmd.AddCommand(queryCmd)

	InfoLogger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLogger = log.New(os.Stdout, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}

type queryData struct {
	Response   string        `json:"response"`
	StatusCode int           `json:"status_code"`
	Endpoint   string        `json:"endpoint"`
	Elapsed    time.Duration `json:"elapsed"`
}

// NewQueryCommand returns a new query cobra command
func newQueryCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "query",
		Short: "Loadtest the nodes provided by sending Pocket Network relays",
		Long: `Send relay requests for the latest block height, to the nodes provided.
The endpoint used will be randomly selected, to simulate a real world scenario.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Trim trailing slashes from the endpoints and add the query path`
			endpoints := make([]string, 0)
			for _, ep := range args {
				endpoint := strings.TrimSuffix(ep, "/")
				endpoints = append(endpoints, endpoint+queryPath)
			}

			// Determine the number of batches to send and the size of the final batch
			numBatches := totalRequests / batchSize
			finalBatchSize := batchSize
			if numBatches*batchSize < totalRequests {
				finalBatchSize = totalRequests - (numBatches * batchSize)
				numBatches += 1
			}

			// Make a batch of requests to a randomly selected endpoint
			for i := 0; i < numBatches; i++ {
				// Randomly select an endpoint
				rand.Seed(time.Now().Unix())
				idx := rand.Intn(len(endpoints))

				size := batchSize
				if i == numBatches-1 {
					size = finalBatchSize
				}

				// Send a batch of requests to the randomly selected endpoint
				batch := batchRequest(makeEmptyPostRequest, size, endpoints[idx])

				// If no output file path was provided, skip writing to file
				if outputFilePath == "" {
					continue
				}

				// Convert to JSON and write to file
				json, _ := json.MarshalIndent(batch, "", " ")
				file, err := os.OpenFile(outputFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				defer file.Close()

				_, err = file.WriteString(string(json))
				if err != nil {
					return err
				}
			}

			return nil
		},
	}
}

// batchRequest makes a batch of requests to the endpoint provided returning data on the request
// and logging any errors that may have occurred
func batchRequest(fn func(string) (*queryData, error), batchSize int, endpoint string) []*queryData {
	InfoLogger.Printf("making %d requests to %s", batchSize, endpoint)
	batchResutls := make([]*queryData, 0)
	wg := sync.WaitGroup{}
	for i := 0; i < batchSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			qd, err := fn(endpoint)
			if err != nil {
				ErrorLogger.Printf("error making request: %s", err.Error())
				return
			}
			batchResutls = append(batchResutls, qd)
		}()
	}
	wg.Wait()
	return batchResutls
}

// makeRequest makes a request to the endpoint provided returning data on the request
// and any errors that may have occurred
func makeEmptyPostRequest(endpoint string) (*queryData, error) {
	r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer([]byte{}))
	if err != nil {
		return nil, err
	}

	r.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	start := time.Now()
	res, err := client.Do(r)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	request := &queryData{
		Response:   strings.TrimSpace(string(b)),
		StatusCode: res.StatusCode,
		Endpoint:   endpoint,
		Elapsed:    elapsed,
	}

	return request, nil
}
