# Xchain Client

Xchain Client provides a set of tools to facilitate cross-chain data movement from Filecoin storage to any blockchain. It includes utilities for managing Ethereum accounts, submitting offers, and handling deal status.

---

## 🚀 Installation

Ensure you have **Go 1.18+** installed. Then, clone the repository and build the project:

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

2. **Source the file** to load the variable into your environment:
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

🔹 This saves the keystore file **at the exact location specified**.
🔹 The file is **password-protected** and should be stored securely.

---

## 🏃‍♂️ Running the Daemon

To start the Xchain adapter daemon, run:

```sh
./xchainClient daemon --config ./config/config.json --buffer-service --aggregation-service
```

---

## 📡 **Submitting an Offer**

To submit an offer to the OnRamp contract:

```sh
./xchainClient client offer <commP> <size> <cid> <bufferLocation> <token-hex> <token-amount>
```

Example:
```sh
./xchainClient client offer bafkreihdwdcef4n... 128 /data/file1 /buffers/ 0x6B175474E89094C44Da98b954EedeAC495271d0F 1000
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

The Xchain Client now **uses a `config.json` file** for storing settings instead of a `.env` file. The configuration file should be placed inside the `config/` directory.

#### **Example `config.json`**
```json
[
  {
    "ChainID": 314159,
    "Api": "wss://wss.calibration.node.glif.io/apigw/lotus/rpc/v1",
    "OnRampAddress": "0x750CbAcFbE58C453cEA1E5a2617193D60B7Cb451",
    "ProverAddr": "0x61F0ACE5ad40466Eb43141fa56Cf87758b6ffbA8",
    "KeyPath": "./config/xchain_key.json",
    "ClientAddr": "0x5c31e78f3f7329769734f5ff1ac7e22c243e817e",
    "PayoutAddr": "0x5c31e78f3f7329769734f5ff1ac7e22c243e817e",
    "OnRampABIPath": "./config/onramp-abi.json",
    "BufferPath": "~/.xchain/buffer",
    "BufferPort": 5077,
    "ProviderAddr": "t0116147",
    "LotusAPI": "https://api.calibration.node.glif.io",
    "LighthouseApiKey": "",
    "LighthouseAuth": ""
  }
]
```

### **Configuration Fields Explained**
| Key | Description |
|------|------------|
| **ChainID** | Ethereum-compatible chain ID (e.g., `314159` for Filecoin Calibration Testnet). |
| **Api** | WebSocket API URL for Ethereum client (e.g., Infura, Glif). |
| **OnRampAddress** | Address of the OnRamp smart contract. |
| **ProverAddr** | Ethereum address of the prover for verifying storage deals. |
| **KeyPath** | Path to the keystore file that contains the Ethereum private key. |
| **ClientAddr** | Ethereum wallet address used for making transactions. |
| **PayoutAddr** | Address where storage rewards should be sent. |
| **OnRampABIPath** | Path to the ABI file for the OnRamp contract. |
| **BufferPath** | Directory where temporary storage is kept before aggregation. |
| **BufferPort** | Port for the buffer service (`5077` by default). |
| **ProviderAddr** | Filecoin storage provider ID. |
| **LotusAPI** | Filecoin Lotus API endpoint (used for deal tracking). |
| **LighthouseApiKey** | API key for interacting with Lighthouse storage (if applicable). |
| **LighthouseAuth** | Authentication token for Lighthouse. |

---

## 🤝 **Contributing**
We welcome contributions! Feel free to submit pull requests or open issues.

---

## 📜 **License**
This project is licensed under the MIT License.
