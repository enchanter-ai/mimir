// Compile helper for the Mimir on-chain anchor contracts.
//
// Reads every .sol under contracts/, compiles with solc-js, and emits ABI +
// creation bytecode under go/abi/ keyed by contract name. The Go test suite
// embeds these via go:embed.
//
// Run after editing any .sol:
//   npm install --no-save solc@0.8.20   (once)
//   node compile.js
const fs   = require("fs");
const path = require("path");
const solc = require("solc");

const contractsDir = path.join(__dirname, "contracts");
const abiDir       = path.join(__dirname, "go/abi");

// All .sol files in contracts/ that we want compiled.
// Exclude Foundry-format .t.sol test files (they depend on forge-std which we don't vendor).
// Real test coverage lives in go/anchor_test.go + go/eigenlayer_test.go against the simulated EVM.
const inputFiles = fs.readdirSync(contractsDir)
  .filter((f) => f.endsWith(".sol") && !f.endsWith(".t.sol"));

const sources = {};
for (const f of inputFiles) {
  sources[f] = { content: fs.readFileSync(path.join(contractsDir, f), "utf8") };
}

// solc-js needs an import callback to resolve "./IEigenLayer.sol" etc.
// Since all our imports are sibling files in contracts/, we just look there.
function findImports(importPath) {
  const candidate = path.join(contractsDir, importPath);
  if (fs.existsSync(candidate)) {
    return { contents: fs.readFileSync(candidate, "utf8") };
  }
  return { error: "File not found: " + importPath };
}

const input = {
  language: "Solidity",
  sources,
  settings: {
    optimizer: { enabled: true, runs: 200 },
    outputSelection: {
      "*": {
        "*": [
          "abi",
          "evm.bytecode.object",                       // creation bytecode (includes constructor)
          "evm.deployedBytecode.object",               // runtime bytecode (what lives on-chain)
          "evm.deployedBytecode.immutableReferences",  // byte positions of immutables (slashWad, etc.)
          "metadata",                                  // CBOR-encoded compiler metadata
        ],
      },
    },
  },
};

const out = JSON.parse(solc.compile(JSON.stringify(input), { import: findImports }));

if (out.errors) {
  const fatal = out.errors.filter((e) => e.severity === "error");
  if (fatal.length) {
    console.error(JSON.stringify(fatal, null, 2));
    process.exit(1);
  }
  for (const w of out.errors) console.warn("[warn]", w.formattedMessage);
}

fs.mkdirSync(abiDir, { recursive: true });

// Emit ABI + bytecode for every concrete contract we built.
// We skip interfaces (no bytecode) and select only contracts we actually deploy.
const deployable = [
  "MimirValidationRegistry",
  "MockServiceManager",
  "MockSlasher",
  "EigenLayerSlasherAdapter",
  "MockAllocationManager",
];

for (const name of deployable) {
  let found = null;
  for (const fileKey of Object.keys(out.contracts)) {
    if (out.contracts[fileKey][name]) {
      found = out.contracts[fileKey][name];
      break;
    }
  }
  if (!found) {
    console.error(`compile: missing contract ${name}`);
    process.exit(1);
  }
  const abi = found.abi;
  const creationHex = "0x" + found.evm.bytecode.object;
  const runtimeHex = "0x" + found.evm.deployedBytecode.object;
  const metadata = found.metadata || "";

  fs.writeFileSync(path.join(abiDir, `${name}.json`), JSON.stringify(abi, null, 2));
  fs.writeFileSync(path.join(abiDir, `${name}.bin`), creationHex);
  fs.writeFileSync(path.join(abiDir, `${name}.runtime.bin`), runtimeHex);
  if (metadata) {
    fs.writeFileSync(path.join(abiDir, `${name}.metadata.json`), metadata);
  }

  // Emit immutable byte ranges so verify-build.sh can mask them when diffing
  // local recompile against on-chain bytecode (Solidity inlines constructor
  // immutable args into runtime bytecode at deploy time; local compile has
  // zeros in those slots).
  const immutables = (found.evm.deployedBytecode.immutableReferences) || {};
  fs.writeFileSync(
    path.join(abiDir, `${name}.immutables.json`),
    JSON.stringify(immutables, null, 2)
  );

  const creationBytes = (creationHex.length - 2) / 2;
  const runtimeBytes  = (runtimeHex.length - 2) / 2;
  const immutableCount = Object.keys(immutables).length;
  console.log(
    `${name.padEnd(28)}  abi+bin+runtime+metadata+immutables  ` +
    `(creation: ${creationBytes} bytes, runtime: ${runtimeBytes} bytes, immutables: ${immutableCount})`
  );
}

