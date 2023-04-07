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
	"fmt"
	"io"
	goLogger "log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/spf13/cobra"
)

// TODO: make this configurable
const (
	queryPath = "/v1/query/height"
)

var (
	logFilePath    string
	outputFilePath string
	batchSize      int
	totalRequests  int
	concurrent     bool
)

func init() {
	queryCmd := newQueryCommand()

	queryCmd.Flags().StringVar(&logFilePath, "log-file", "", "log file path (defaults to \"\" (stdout))")
	queryCmd.Flags().StringVar(&outputFilePath, "output-file", "query-results.json", "query data output file path")
	queryCmd.Flags().IntVarP(&batchSize, "batch-size", "b", 100, "number of requests to send in each batch")
	queryCmd.Flags().IntVarP(&totalRequests, "total-requests", "t", 10000, "number of requests to send in each batch")
	queryCmd.Flags().BoolVar(&concurrent, "concurrent", false, "send requests to all endpoints concurrently")

	ctx := rootCmd.Context()
	queryCmd.SetContext(ctx)

	rootCmd.AddCommand(queryCmd)
}

type queryData struct {
	Response   string        `json:"response"`
	StatusCode int           `json:"status_code"`
	Error      string        `json:"error"`
	Endpoint   string        `json:"endpoint"`
	Elapsed    time.Duration `json:"elapsed"`
}

// NewQueryCommand returns a new query cobra command
func newQueryCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "query <endpoint> <endpoint> ... <endpoint>",
		Short: "Loadtest the nodes provided by sending Pocket Network relays",
		Long: `Send relay requests for the latest block height, to the nodes provided.
The endpoint used will be randomly selected, to simulate a real world scenario.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			setupLogging()

			// Trim trailing slashes from the endpoints and add the query path`
			endpoints := make([]string, 0)
			for _, ep := range args {
				endpoint := strings.TrimSuffix(ep, "/")
				endpoints = append(endpoints, endpoint+queryPath)
			}

			// Make a batch of requests to a randomly selected endpoint
			// Run the loadtest
			if concurrent {
				// Get batch info for each endpoint
				numBatches, batchSize, finalBatchSize := getConcurrentBatchInfo(endpoints)

				// Run the loadtest concurrently
				concurrentRequests(endpoints, numBatches, batchSize, finalBatchSize)
			} else {
				// Get batch info
				numBatches, batchSize, finalBatchSize := getBatchInfo()

				log.WithFields(
					log.Fields{
						"endpoints":      endpoints,
						"total_requests": totalRequests,
						"batch_size":     batchSize,
						"num_batches":    numBatches,
						"concurrent":     concurrent,
					},
				).Info("starting to loadtest endpoints sequentially")

				// Run the loadtest sequentially
				err := sequentialRequests(endpoints, numBatches, batchSize, finalBatchSize)
				if err != nil {
					log.WithFields(
						log.Fields{
							"error": err.Error(),
						},
					).Error("error running sequential requests")
				}
			}

			return nil
		},
	}
}

// setupLogging sets up the logging for the application to use either stdout or the logfile provided
func setupLogging() {
	logFile := os.Stdout
	var err error
	if logFilePath != "" {
		logFile, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			goLogger.Fatalf("unable to open log file: %s", err.Error())
		}
	}

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetOutput(logFile)
}

// getBatchInfo determines the number of batches to send and the size of the final batch
func getBatchInfo() (int, int, int) {
	numBatches := totalRequests / batchSize
	finalBatchSize := batchSize
	if numBatches*batchSize < totalRequests {
		finalBatchSize = totalRequests - (numBatches * batchSize)
		numBatches += 1
	}

	return numBatches, batchSize, finalBatchSize
}

// getConcurrentBatchInfo determines the number of batches and their size to send to all endpoints concurrently
// TODO: Add better batch size calculation
func getConcurrentBatchInfo(endpoints []string) (int, int, int) {
	numEndpoints := len(endpoints)
	totalPerEndpoint := totalRequests / numEndpoints
	numBatches := totalPerEndpoint / batchSize
	finalBatchSize := batchSize
	if numBatches*batchSize < totalPerEndpoint {
		finalBatchSize = totalPerEndpoint - (numBatches * batchSize)
		numBatches += 1
	}
	return numBatches, batchSize, finalBatchSize
}

// concurrentRequests makes batches of requests to all endpoints concurrently
func concurrentRequests(endpoints []string, numBatches, batchSize, finalBatchSize int) {
	// Create a wait group to wait for all requests to finish
	wg := sync.WaitGroup{}
	for i := 0; i < len(endpoints); i++ {
		wg.Add(1)
		// Log the loadtest info for each endpoint
		log.WithFields(
			log.Fields{
				"endpoint":       endpoints[i],
				"total_requests": ((numBatches - 1) * batchSize) + finalBatchSize,
				"batch_size":     batchSize,
				"num_batches":    numBatches,
				"concurrent":     concurrent,
			},
		).Info("starting to loadtest endpoints concurrently")

		go func(i int) {
			defer wg.Done()
			// Make requests to the endpoint sequentially in a goroutine
			err := sequentialRequests(endpoints[i:i+1], numBatches, batchSize, finalBatchSize)
			if err != nil {
				log.WithFields(
					log.Fields{
						"error": err.Error(),
					},
				).Error("error running sequential requests")
			}
			return
		}(i)
	}
	wg.Wait()

	return
}

// sequentialRequests makes batches of requests to a randomly selected endpoint sequentially
func sequentialRequests(endpoints []string, numBatches, batchSize, finalBatchSize int) error {
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
		json, _ := json.Marshal(batch)
		outputFile, err := os.OpenFile(outputFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		defer outputFile.Close()

		_, err = outputFile.WriteString(fmt.Sprintf("%s\n", string(json)))
		if err != nil {
			return err
		}
	}

	return nil
}

// batchRequest makes a batch of requests to the endpoint provided returning data on the requests
func batchRequest(fn func(string) *queryData, batchSize int, endpoint string) []*queryData {
	// Log batch info
	log.WithFields(
		log.Fields{
			"batch_size": batchSize,
			"endpoint":   endpoint,
		},
	).Infof("making batch of requests")

	// Create a wait group to wait for all requests to finish
	wg := sync.WaitGroup{}
	batchResutls := make([]*queryData, 0)
	for i := 0; i < batchSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Make request
			qd := fn(endpoint)
			batchResutls = append(batchResutls, qd)
			return
		}()
	}
	wg.Wait()

	return batchResutls
}

// makeRequest makes a request to the endpoint provided returning data on the request
// and any errors that may have occurred
func makeEmptyPostRequest(endpoint string) *queryData {
	start := time.Now()
	client := &http.Client{}
	res, err := client.Post(endpoint, "application/json", bytes.NewBuffer([]byte{}))
	if err != nil {
		return &queryData{
			StatusCode: res.StatusCode,
			Error:      err.Error(),
			Endpoint:   endpoint,
		}
	}
	defer res.Body.Close()
	elapsed := time.Since(start)

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return &queryData{
			StatusCode: res.StatusCode,
			Error:      err.Error(),
			Endpoint:   endpoint,
		}
	}

	return &queryData{
		Response:   strings.TrimSpace(string(b)),
		StatusCode: res.StatusCode,
		Endpoint:   endpoint,
		Elapsed:    elapsed,
	}
}
