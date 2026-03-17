rm -rf datadir1

export GETH_COORDINATOR_KEY="0x4ffc57431830b53596f4a3f275c5e9193b5c5d428405773bcfd0d57a40ec6af9"

# Initialize the local rollup using the hoodi-dev/rollup-a L2 chain ID.
build/bin/geth \
  --networkid=61110 \
  --registry.path=./compose-registry \
  init \
  --state.scheme=hash \
  --datadir=datadir1 \
  genesis-1.json


  #--override.osaka=0 \
