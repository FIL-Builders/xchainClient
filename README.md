# Xchain Client

Xchain Client provides a set of tools to facilitate cross-chain data movement from Filecoin storage to any blockchain. It includes utilities for managing Ethereum accounts, submitting offers, and handling deal status.

---

## 🚀 Installation

Ensure you have **Go** installed. Then, clone the repository and build the project:

```sh
git clone https://github.com/your-repo/xchainClient.git
cd xchainClient
go build -o xchainClient
```
---

## 📌 Configure Environment Variables
1. Create a `.env` file to store your **XCHAIN_PASSPHRASE** (used for unlocking the Ethereum keystore):
    ```sh
    echo "export XCHAIN_PASSPHRASE=your_secure_password" > .env
    ```

2. **Source the file** to load the variable into your environment before you run any xchainClient commands:
   ```sh
   source .env
   ```

3. Verify that the variable is set:
   ```sh
   echo $XCHAIN_PASSPHRASE
   ```

## 🔑 **Generating an Ethereum Account**

A new command, `generate-account`, allows you to create an Ethereum keystore account and store it at a **specific file path**.

```sh
./xchainClient generate-account --keystore-file ./config/xchain_key.json --password "$XCHAIN_PASSPHRASE"

```

### **Example Output**
```
New Ethereum account created!
Address: 0x123456789abcdef...
Keystore File Path: /home/user/onramp-contracts/xchain_key.json
```

🔹 This saves the keystore file **at the exact location specified**. The file is **password-protected** and should be stored securely.

🔹 To run xChainClient for a specific chain, you need to request some test token to this wallet address from that chain. 

---

## 🏃‍♂️ Running the Daemon

Once you have finisehd the above process and sucessfully deployed [onramp contracts](https://github.com/FIL-Builders/onramp-contracts) to the source chain and Filecoin. You should update the `config.json` with the correct information.

To start the Xchain adapter daemon, run:

```sh
./xchainClient daemon --config ./config/config.json --chain avalanche --buffer-service --aggregation-service
```

---

## 📡 **offering data with automatic car processing**

the `offer-file` command simplifies offering data by automatically:

1. converting the file into a car format.
2. calculating the commp (content identifier for proofs).
3. uploading the car file to a local buffer service.
4. submitting an offer transaction to the blockchain.

```sh
./xchainclient client offer-file --chain avalanche --config ./config/config.json <file_path> <payment-addr> <payment-amount>
```

example:

```sh
./xchainclient client offer-file --chain avalanche ./data/sample.txt 0x5c31e78f3f7329769734f5ff1ac7e22c243e817e 1000
```

---

## 📡 **submitting an offer (manual method)**

to submit an offer to the onramp contract manually:

```sh
./xchainclient client offer <commp> <size> <cid> <bufferlocation> <token-hex> <token-amount>
```

example:

```sh
./xchainclient client offer bafkreihdwdcef4n... 128 /data/file1 /buffers/ 0x6b175474e89094c44da98b954eedeac495271d0f 1000
```

---

## 🔍 **Checking Deal Status**

To check the deal status for a CID:

```sh
./xchainClient client dealStatus <cid> <offerId>
```

Example:
```sh
./xchainClient client dealStatus bafkreihdwdcef4n 42
```

---

## 📖 **Additional Notes**
- **Keep your `config.json` file secure** since it contains sensitive information like private key paths and authentication tokens.
- **Use strong passwords** when generating Ethereum accounts.
- **Regularly back up keystore files** to avoid losing access to funds.

---

## 💡 **Troubleshooting**
### Error: "config.json not found"
Ensure the config file is correctly placed in the `config/` directory and named `config.json`.

### Error: "invalid keystore file"
Ensure the keystore file is correctly generated using `generate-account` and that you are using the correct password.

### Error: "failed to connect to API"
Check that your `Api` field in `config.json` is correctly set to a working Ethereum/Web3 provider.

---
## 🛠️ Configuration

### **Config File (`config.json`)**

The Xchain Client uses a `config.json` file to store its settings. The configuration file should be placed inside the `config/` directory.

#### **Example `config.json`**
```json
{
  "destination": {
    "ChainID": 314159,
    "LotusAPI": "https://api.calibration.node.glif.io",
    "ProverAddr": "0x61F0ACE5ad40466Eb43141fa56Cf87758b6ffbA8"
  },
  "sources": {
    "filecoin": {
      "Api": "wss://wss.calibration.node.glif.io/apigw/lotus/rpc/v1",
      "OnRampAddress": "0x750CbAcFbE58C453cEA1E5a2617193D60B7Cb451"
    },
    "avalanche": {
      "Api": "wss://api.avax-test.network/ext/bc/C/ws",
      "OnRampAddress": "0x123...abc"
    },
    "polygon": {
      "Api": "wss://polygon-rpc.com",
      "OnRampAddress": "0x456...def"
    }
  },
  "KeyPath": "./config/xchain_key.json",
  "ClientAddr": "0x5c31e78f3f7329769734f5ff1ac7e22c243e817e",
  "PayoutAddr": "0x5c31e78f3f7329769734f5ff1ac7e22c243e817e",
  "OnRampABIPath": "./config/onramp-abi.json",
  "BufferPath": "~/.xchain/buffer",
  "BufferPort": 5077,
  "ProviderAddr": "t0116147",
  "LighthouseApiKey": "",
  "LighthouseAuth": "",
  "TransferIP": "0.0.0.0",
  "TransferPort": 9999,
  "TargetAggSize": 0
}
```

### **Configuration Fields Explained**
| Key | Description |
|------|------------|
| **destination.ChainID** | Ethereum-compatible chain ID for the destination network. |
| **destination.LotusAPI** | Filecoin Lotus API endpoint used for deal tracking. |
| **destination.ProverAddr** | Ethereum address of the prover verifying storage deals. |
| **sources.filecoin.Api** | WebSocket API for Filecoin calibration network. |
| **sources.filecoin.OnRampAddress** | Filecoin OnRamp contract address. |
| **sources.avalanche.Api** | WebSocket API for Avalanche network. |
| **sources.avalanche.OnRampAddress** | Avalanche OnRamp contract address. |
| **sources.polygon.Api** | WebSocket API for Polygon network. |
| **sources.polygon.OnRampAddress** | Polygon OnRamp contract address. |
| **KeyPath** | Path to the keystore file that contains the Ethereum private key. |
| **ClientAddr** | Ethereum wallet address used for making transactions. |
| **PayoutAddr** | Address where storage rewards should be sent. |
| **OnRampABIPath** | Path to the ABI file for the OnRamp contract. |
| **BufferPath** | Directory where temporary storage is kept before aggregation. |
| **BufferPort** | Port for the buffer service (`5077` by default). |
| **ProviderAddr** | Filecoin storage provider ID. |
| **LighthouseApiKey** | API key for interacting with Lighthouse storage (if applicable). |
| **LighthouseAuth** | Authentication token for Lighthouse. |
| **TransferIP** | IP address for cross-chain data transfer service (`0.0.0.0` for all interfaces). |
| **TransferPort** | Port for the cross-chain data transfer service (`9999` by default). |
| **TargetAggSize** | Specifies the aggregation size for deal bundling (currently set to `0`). |

### **Multi-Chain Support**
Xchain Client supports interaction with multiple blockchains. Users can configure multiple `sources` to enable cross-chain deal submissions. Supported networks include:
- **Filecoin**
- **Avalanche**
- **Polygon**

Each source requires an **API endpoint** and an **OnRamp contract address**, which are specified under the `sources` field in `config.json`.

---

## 🤝 **Contributing**
We welcome contributions! Feel free to submit pull requests or open issues.

---

## 📜 **License**
This project is licensed under the MIT License.
