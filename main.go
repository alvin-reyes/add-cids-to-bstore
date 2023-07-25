package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/application-research/whypfs-core"
	"github.com/cheggaaa/pb/v3"
	commcid "github.com/filecoin-project/go-fil-commcid"
	commp "github.com/filecoin-project/go-fil-commp-hashhash"
	"github.com/filecoin-project/go-fil-markets/shared"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/boxo/ipld/car"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	uio "github.com/ipfs/go-unixfs/io"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"io"
	"io/ioutil"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://bafybeifcghbafml4yrk43m3pvplin4auibnwrdv5v3rnwnovjjpkt6tkju.ipfs.dweb.link/"

type Peer struct {
	ID    string
	Addrs []string
}

var node *whypfs.Node

func main() {

	repo := flag.String("repo", "./whypfs", "path to the repo")
	cidsUrlSource := flag.String("cids-url-source", baseURL, "URL to fetch cids.txt from")
	peers := flag.String("peers", "[{\"ID\":\"12D3KooWB5HcweB1wdgK8bjfTRHcZdvMFd6ffrn6XqMMyUG7pakP\",\"Addrs\":[\"/dns/bacalhau.dokterbob.net/tcp/4001\",\"/dns/bacalhau.dokterbob.net/udp/4001/quic\"]}]", "comma-separated list of peers to connect to")

	// unmarshal the peers string to an array of Peer structs
	peerList := make([]Peer, 0)
	err := json.Unmarshal([]byte(*peers), &peerList)
	if err != nil {
		fmt.Printf("An error occurred while parsing the peers string: %s\n", err)
		return
	}

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

	nodeW, err := NewEdgeNode(context.Background(), *repo)
	if err != nil {
		fmt.Printf("Error occurred while creating a new node: %s\n", err)
		return
	}
	node = nodeW

	// for each peerList, convert it to peer.AddrInfo
	var peerInfos []peer.AddrInfo
	for _, peerItem := range peerList {
		// create peer id
		peerId, err := peer.Decode(peerItem.ID)
		if err != nil {
			fmt.Printf("Error occurred while decoding peer ID: %s\n", err)
			return
		}
		// create multiaddr array
		var multiAddrs []multiaddr.Multiaddr
		for _, addr := range peerItem.Addrs {
			multiAddr, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				fmt.Printf("Error occurred while creating multiaddr: %s\n", err)
				return
			}
			multiAddrs = append(multiAddrs, multiAddr)
		}

		peerInfo := peer.AddrInfo{
			ID:    peerId,
			Addrs: multiAddrs,
		}
		peerInfos = append(peerInfos, peerInfo)
	}

	// connect
	ConnectToDelegates(context.Background(), *node, peerInfos)

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

	// Create a WaitGroup to wait for all batches to finish
	var allBatchesWG sync.WaitGroup
	allBatchesWG.Add(len(cids) / batchSizePerCPU)

	// Create a semaphore channel to limit the number of goroutines
	sem := make(chan struct{}, concurrentLimit)

	// Divide the CIDs into batches
	batches := splitIntoBatches(cids, batchSizePerCPU)

	bucket := &CIDBucket{
		maxSize: 4 * 1024 * 1024, // 4 MB
	}

	// Process each batch sequentially
	for _, batch := range batches {
		fmt.Printf("Processing batch of %d CIDs\n", len(batch))

		// Create a map to store the progress bars for each CID in the current batch
		bars := make(map[string]*pb.ProgressBar)

		// Create a WaitGroup to wait for all goroutines in the current batch to finish
		var wg sync.WaitGroup

		// Launch goroutines
		wg.Add(len(batch))
		for _, cidItem := range batch {
			go fetchCID(cidItem, node, results, &wg, sem, bars[cidItem], *bucket)
			<-results
		}
		wg.Wait()

		////<-sem // Wait for a free slot in the semaphore channel
		allBatchesWG.Done()
		fmt.Println("Finished processing batch")
	}

	// Wait for all batches to finish before closing the results channel
	allBatchesWG.Wait()
	close(results)

	// Collect errors from the results channel
	for err := range results {
		if err != nil {
			fmt.Printf("Error fetching CID: %s\n", err)
		}
	}

	return
}

type CIDBucket struct {
	mu      sync.Mutex
	cids    []string
	size    int64
	maxSize int64 // The maximum size of the bucket in bytes (4GB in this case)
}

// GetCidBuilderDefault is a helper function that returns a default cid builder
func GetCidBuilderDefault() cid.Builder {
	cidBuilder, err := merkledag.PrefixForCidVersion(1)
	if err != nil {
		panic(err)
	}
	cidBuilder.MhType = uint64(multihash.SHA2_256)
	cidBuilder.MhLength = -1
	return cidBuilder
}

func (bucket *CIDBucket) addCID(cidS string, size int64) {
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Check if adding this CID exceeds the maximum size
	if bucket.size+size > bucket.maxSize {

		// car the bucket
		// for each content, generate a node and a raw
		dir := uio.NewDirectory(node.DAGService)
		dir.SetCidBuilder(GetCidBuilderDefault())
		buf := new(bytes.Buffer)
		fmt.Println(len(bucket.cids))
		for _, cAgg := range bucket.cids {
			cCidAgg, err := cid.Decode(cAgg)
			if err != nil {
				return
			}

			cDataAgg, errCData := node.Get(context.Background(), cCidAgg) // get the node
			if errCData != nil {
				return
			}

			_, err = io.Copy(buf, bytes.NewReader(cDataAgg.RawData()))
			dir.AddChild(context.Background(), cAgg, cDataAgg)
		}

		dirNode, err := dir.GetNode()
		if err != nil {
			return
		}
		node.Blockstore.Put(context.Background(), dirNode)
		if err != nil {
			return
		}

		pieceCid, carSize, unpaddedPieceSize, bufFile, err := GeneratePieceCommitment(context.Background(), dirNode.Cid(), node.Blockstore)
		bufFileN, err := node.AddPinFile(context.Background(), &bufFile, nil)

		fmt.Println("Piece CID: ", pieceCid)
		fmt.Println("CAR size: ", carSize)
		fmt.Println("Unpadded piece size: ", unpaddedPieceSize)
		fmt.Println("File size: ", bufFile.Len())
		fmt.Println("Car Cid", bufFileN.Cid())

		// Reset the bucket
		bucket.cids = []string{}
		bucket.size = 0
	}

	bucket.cids = append(bucket.cids, cidS)
	bucket.size += size
}

const maxTraversalLinks = 32 * (1 << 20)

func GeneratePieceCommitment(ctx context.Context, payloadCid cid.Cid, bstore blockstore.Blockstore) (cid.Cid, uint64, abi.UnpaddedPieceSize, bytes.Buffer, error) {
	selectiveCar := car.NewSelectiveCar(
		context.Background(),
		bstore,
		[]car.Dag{{Root: payloadCid, Selector: shared.AllSelector()}},
		car.MaxTraversalLinks(maxTraversalLinks),
		car.TraverseLinksOnlyOnce(),
	)

	buf := new(bytes.Buffer)
	blockCount := 0
	var oneStepBlocks []car.Block
	err := selectiveCar.Write(buf, func(block car.Block) error {
		oneStepBlocks = append(oneStepBlocks, block)
		blockCount++
		return nil
	})
	if err != nil {
		return cid.Undef, 0, 0, *buf, err
	}

	preparedCar, err := selectiveCar.Prepare()
	if err != nil {
		return cid.Undef, 0, 0, *buf, err
	}

	writer := new(commp.Calc)
	carWriter := &bytes.Buffer{}
	err = preparedCar.Dump(ctx, writer)
	if err != nil {
		return cid.Undef, 0, 0, *buf, err
	}
	commpc, size, err := writer.Digest()
	if err != nil {
		return cid.Undef, 0, 0, *buf, err
	}
	err = preparedCar.Dump(ctx, carWriter)
	if err != nil {
		return cid.Undef, 0, 0, *buf, err
	}

	commCid, err := commcid.DataCommitmentV1ToCID(commpc)
	if err != nil {
		return cid.Undef, 0, 0, *buf, err
	}

	return commCid, preparedCar.Size(), abi.PaddedPieceSize(size).Unpadded(), *buf, nil
}

func fetchCID(cidItem string, node *whypfs.Node, results chan<- error, wg *sync.WaitGroup, sem chan struct{}, bar *pb.ProgressBar, bucket CIDBucket) {
	defer wg.Done()

	// Acquire the semaphore, this will block if the semaphore is full
	sem <- struct{}{}
	defer func() {
		// Release the semaphore after finishing the work
		<-sem
	}()

	cidD, err := cid.Decode(cidItem)
	if err != nil {
		results <- fmt.Errorf("Error decoding cid: %s", err)
		return
	}
	fmt.Print("Fetching CID: ", cidItem)
	nd, errF := node.Get(context.Background(), cidD)
	if errF != nil {
		results <- fmt.Errorf("error getting cid: %s", err)
	}
	ndSize, errS := nd.Size()
	if errS != nil {
		results <- fmt.Errorf("error getting cid: %s", errS)
		return
	}
	fmt.Println(" Size: ", ndSize)
	results <- nil
	bucket.addCID(cidItem, int64(ndSize))
}

// splitIntoBatches splits the list of CIDs into batches of the specified batch size.
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

func ConnectToDelegates(ctx context.Context, node whypfs.Node, peerInfos []peer.AddrInfo) error {

	for _, peerInfo := range peerInfos {
		node.Host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour)

		if node.Host.Network().Connectedness(peerInfo.ID) != network.Connected {
			if err := node.Host.Connect(ctx, peer.AddrInfo{
				ID: peerInfo.ID,
			}); err != nil {
				return err
			}

			node.Host.ConnManager().Protect(peerInfo.ID, "pinning")
		}
	}

	return nil
}
