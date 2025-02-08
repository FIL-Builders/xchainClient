# xchainClient
The xchain clients takes care of data bridge events

### **1️⃣ Set Up Forge**
```sh
forge install
```

### **2️⃣ Install & Use Go 1.22.7**
```sh
gvm install go1.22.7
gvm use go1.22.7
```

### **3️⃣ Build OnRamp Tools**
```sh
go build
```

### **4️⃣ Generate Cross-Chain Keys**
🔑 **Use xchainClient to create an Ethereum account for signing transactions**
```sh
./xchainClient generate-account --keystore-folder ~/onramp-contracts --password "securepassword123"
```
Example output:
```
New Ethereum account created!
Address: 0x01a21f71e5937759f08e72cF2FD99C5Ca14E55b3
Keystore File Path: /Users/USER/onramp-contracts/UTC--2025-02-08T01-38-38.942688000Z--01a21f71e5937759f08e72cf2fd99c5ca14e55b3
```

Set environment variables:
```sh
export XCHAIN_KEY_PATH=~/onramp-contracts/xchain_key.json/UTC--2024-10-01T21-31-48.090887441Z--your-address
export XCHAIN_PASSPHRASE=password
export XCHAIN_ETH_API="http://127.0.0.1:1234/rpc/v1"
export MINER_ADDRESS=t01013
```

---

## **🚀 Running XChain**
Set environment variables as above, then:
```sh
./contract-tools/xchain/xchain_server
```
Use the XChain client to upload data:
```sh
./contract-tools/client.bash screenshot.png 0xaEE9C9E8E4b40665338BD8374D8D473Bd014D1A1 1
```

---

## **🔍 Additional Notes & References**
- [Shashank's Guide](https://gist.github.com/lordshashank/fb2fbd53b5520a862bd451e3603b4718)
- [Filecoin Deals Repo](https://github.com/lordshashank/filecoin-deals)
