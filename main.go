package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/application-research/whypfs-core"
	"github.com/cheggaaa/pb/v3"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	"io/ioutil"
	"net/http"
	"runtime"
	"strings"
	"sync"
)

const baseURL = "https://bafybeifcghbafml4yrk43m3pvplin4auibnwrdv5v3rnwnovjjpkt6tkju.ipfs.dweb.link/"

func main() {

	repo := flag.String("repo", "./whypfs", "path to the repo")
	cidsUrlSource := flag.String("cids-url-source", baseURL, "URL to fetch cids.txt from")

	// Parse the command-line flags.
	flag.Parse()

	resp, err := http.Get(*cidsUrlSource)
	if err != nil {
		fmt.Printf("An error occurred while fetching cids.txt: %s\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("An error occurred while fetching cids.txt: %s\n", resp.Status)
		return
	}

	cidsBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error occurred while reading cids.txt: %s\n", err)
		return
	}

	cidsStr := string(cidsBytes)
	cids := strings.Split(strings.TrimSpace(cidsStr), "\n")

	node, err := NewEdgeNode(context.Background(), *repo)
	if err != nil {
		fmt.Printf("Error occurred while creating a new node: %s\n", err)
		return
	}

	fmt.Println("List of CIDs:")
	// Number of concurrent goroutines based on the number of CPUs available
	concurrentLimit := runtime.NumCPU()

	// Calculate the batch size per CPU
	batchSizePerCPU := len(cids) / concurrentLimit
	if batchSizePerCPU == 0 {
		batchSizePerCPU = 1 // Ensure there's at least one CID per batch
	}

	// Create a channel to receive errors from goroutines
	results := make(chan error)

	// Create a WaitGroup to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Create a semaphore channel to limit the number of goroutines
	sem := make(chan struct{}, concurrentLimit)

	// Create a map to store the progress bars for each CID
	bars := make(map[string]*pb.ProgressBar)

	// Divide the CIDs into batches
	fmt.Println("Dividing CIDs into batches")
	fmt.Println("Batch size per CPU: ", batchSizePerCPU)
	fmt.Println("Total number of batches: ", len(cids)/batchSizePerCPU)
	fmt.Println("Total number of CID items: ", len(cids))
	batches := splitIntoBatches(cids, batchSizePerCPU)

	// Process each batch sequentially
	for _, batch := range batches {
		fmt.Printf("Processing batch of %d CIDs\n", len(batch))
		// Launch goroutines
		for _, cidItem := range batch {
			wg.Add(1)
			// Create a progress bar for each CID
			bar := pb.New64(0) // Start with 0 bytes
			//bar.SetTemplateString(`{{string . "prefix"}}{{counters . }}{{bar . }} {{percent . }} ({{speed . "%sB/s"}})`)
			bar.Start()
			bars[cidItem] = bar
			go fetchCID(cidItem, node, results, &wg, sem, bars[cidItem])
		}

		// Wait for all goroutines in the current batch to finish
		wg.Wait()

		// Close the progress bars for the current batch
		for _, cidItem := range batch {
			bars[cidItem].Finish()
		}
	}

	close(results)

	// Collect errors from the results channel
	for err := range results {
		if err != nil {
			fmt.Printf("Error fetching CID: %s\n", err)
		}
	}
}

func fetchCID(cidItem string, node *whypfs.Node, results chan<- error, wg *sync.WaitGroup, sem chan struct{}, bar *pb.ProgressBar) {
	defer wg.Done()

	// Acquire the semaphore, this will block if the semaphore is full
	sem <- struct{}{}
	defer func() {
		// Release the semaphore after finishing the work
		<-sem
	}()

	fmt.Println("Fetching CID: ", cidItem)
	cidD, err := cid.Decode(cidItem)
	if err != nil {
		results <- fmt.Errorf("Error decoding cid: %s", err)
		return
	}

	_, errF := node.Get(context.Background(), cidD)

	// Increment the progress bar
	bar.Increment()

	results <- errF
}
func splitIntoBatches(cids []string, batchSize int) [][]string {
	var batches [][]string
	for i := 0; i < len(cids); i += batchSize {
		end := i + batchSize
		if end > len(cids) {
			end = len(cids)
		}
		batch := cids[i:end]
		batches = append(batches, batch)
	}
	return batches
}
func NewEdgeNode(ctx context.Context, repo string) (*whypfs.Node, error) {

	// node
	publicIp, err := GetPublicIP()
	newConfig := &whypfs.Config{
		ListenAddrs: []string{
			"/ip4/0.0.0.0/tcp/6745",
			"/ip4/0.0.0.0/udp/6746/quic",
			"/ip4/" + publicIp + "/tcp/6745",
		},
		AnnounceAddrs: []string{
			"/ip4/0.0.0.0/tcp/6745",
			"/ip4/" + publicIp + "/tcp/6745",
		},
	}

	ds := dsync.MutexWrap(datastore.NewMapDatastore())
	//ds, err := levelds.NewDatastore(cfg.Node.DsRepo, nil)
	if err != nil {
		panic(err)
	}
	params := whypfs.NewNodeParams{
		Ctx:       ctx,
		Datastore: ds,
		Repo:      repo,
	}

	params.Config = params.ConfigurationBuilder(newConfig)
	whypfsPeer, err := whypfs.NewNode(params)
	if err != nil {
		panic(err)
	}

	// read the cid text
	return whypfsPeer, nil

}

func GetPublicIP() (string, error) {
	resp, err := http.Get("https://ifconfig.me") // important to get the public ip if possible.
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
