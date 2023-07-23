package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/application-research/whypfs-core"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	"io/ioutil"
	"net/http"
	"strings"
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
	for _, cidItem := range cids {
		fmt.Println(cidItem)
		cidD, err := cid.Decode(cidItem)
		if err != nil {
			fmt.Printf("Error decoding cid: %s\n", err)
			return
		}
		nodeBlock, errN := blocks.NewBlockWithCid(cidD.Bytes(), cidD)
		if errN != nil {
			fmt.Printf("Error creating block: %s\n", errN)
			return
		}
		errNb := node.Blockstore.Put(context.Background(), nodeBlock)
		if errNb != nil {
			fmt.Printf("Error putting block: %s\n", errNb)
			return
		}

		// fetch
		fmt.Println("Fetching CID: ", cidItem)
		_, errF := node.Blockstore.Get(context.Background(), cidD)
		if errF != nil {
			fmt.Printf("Error fetching CID: %s\n", errF)
			return
		}

		fmt.Println("Fetched CID: ", cidItem)

	}

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
