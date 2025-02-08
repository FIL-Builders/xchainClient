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


