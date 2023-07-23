# Fetch CIDs to delta/edge compatible blockstore
Fetch a list of CIDs (Content Identifier) from a specific URL and download the corresponding files from the IPFS (InterPlanetary File System) network. The downloaded files will be stored in a delta/edge compatible blockstore, which is a data structure used to manage content-addressable data efficiently.

## Steps:

- Fetch the list of CIDs from a designated URL (e.g., https://example.com/cids.txt) and store them in a file named cids.txt.
- Sample list of CIDs: https://bafybeifcghbafml4yrk43m3pvplin4auibnwrdv5v3rnwnovjjpkt6tkju.ipfs.dweb.link/
- Loop through each CID in cids.txt, download the corresponding file from the IPFS network using the https://dweb.link/ipfs/ base URL, and show a progress bar for each download.
- If there is an error while downloading a file for a specific CID, record the problematic CID in a list of failed CIDs.
- After successfully downloading all the valid files, print the list of CIDs that encountered errors during the download process.
- Store the downloaded files in a delta/edge compatible blockstore for efficient management and future use.
  
## Install
```
go build -o addc
```

## Running
```
./addc --repo=<existing delta or edge blockstore> --cids-url-source="https://bafybeifcghbafml4yrk43m3pvplin4auibnwrdv5v3rnwnovjjpkt6tkju.ipfs.dweb.link/"
```

## Author
Protocol Labs Outercore Engineering.
