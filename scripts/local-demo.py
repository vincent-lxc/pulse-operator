#!/usr/bin/env python3
"""Local PolicyVault integration demo. No private keys or external RPCs.

Starts its own silent Anvil on a random loopback port, deploys MockERC20 and
PolicyVault using unlocked development accounts, and launches the Go CLI as
separate processes to verify restart recovery. Cleans up the node on exit.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--foundry-bin", help="directory containing forge/anvil/cast")
    parser.add_argument("--operator-bin", help="existing operator CLI binary")
    parser.add_argument("--output", help="write non-secret JSON verification summary")
    args = parser.parse_args()
    tools = {}
    for name in ("forge", "anvil", "cast"):
        found = str(Path(args.foundry_bin).resolve() / name) if args.foundry_bin else shutil.which(name)
        if not found or not Path(found).is_file():
            raise RuntimeError(f"{name} required; install Foundry or pass --foundry-bin")
        tools[name] = found
    # Never pass ambient wallet/model credentials into subprocesses; no dotenv.
    env = {k: os.environ[k] for k in ("PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPROXY", "TMPDIR") if k in os.environ}
    env["CGO_ENABLED"] = "0"
    subprocess.run([tools["forge"], "build", "--root", str(ROOT / "contracts")], env=env, check=True, stdout=subprocess.DEVNULL)
    with tempfile.TemporaryDirectory(prefix="pulse-operator-local-") as tmp:
        tmp = Path(tmp)
        binary = Path(args.operator_bin).resolve() if args.operator_bin else tmp / "operator"
        if not args.operator_bin:
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/operator"], cwd=ROOT / "agent", env=env, check=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        endpoint = f"http://127.0.0.1:{port}"
        # Silence Anvil's startup account/private-key listing. Only eth_accounts
        # addresses are used. No private key is read, printed, saved or signed.
        node = subprocess.Popen([tools["anvil"], "--silent", "--host", "127.0.0.1", "--port", str(port), "--chain-id", "31337"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

        def rpc(method, params):
            payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
            req = urllib.request.Request(endpoint, data=payload, headers={"Content-Type": "application/json"})
            with opener.open(req, timeout=15) as response:
                body = json.load(response)
            if "error" in body:
                raise RuntimeError(f"{method}: {body['error']}")
            return body["result"]

        def calldata(signature, *values):
            return subprocess.check_output([tools["cast"], "calldata", signature, *map(str, values)], text=True, env=env).strip()

        def transact(sender, data, to=None):
            assert int(rpc("eth_chainId", []), 16) == 31337
            tx = {"from": sender, "data": data, "gas": hex(8_000_000)}
            if to:
                tx["to"] = to
            txhash = rpc("eth_sendTransaction", [tx])
            for _ in range(200):
                receipt = rpc("eth_getTransactionReceipt", [txhash])
                if receipt:
                    if int(receipt["status"], 16) != 1:
                        raise RuntimeError(f"local transaction reverted: {txhash}")
                    return receipt
                time.sleep(0.05)
            raise RuntimeError("local receipt timed out")

        def read(signature, to, *values):
            return rpc("eth_call", [{"to": to, "data": calldata(signature, *values)}, "latest"])

        try:
            for _ in range(100):
                if node.poll() is not None:
                    raise RuntimeError("Anvil exited during startup")
                try:
                    if int(rpc("eth_chainId", []), 16) == 31337:
                        break
                except (OSError, RuntimeError):
                    time.sleep(0.05)
            else:
                raise RuntimeError("Anvil startup timed out")
            owner, agent, payee, reserve = rpc("eth_accounts", [])[:4]

            def deploy(artifact, constructor=""):
                obj = json.loads((ROOT / "contracts" / "out" / artifact).read_text())
                return transact(owner, obj["bytecode"]["object"] + constructor)["contractAddress"]

            token = deploy("MockERC20.sol/MockERC20.json")
            constructor = "".join(a[2:].lower().rjust(64, "0") for a in (token, owner, agent, reserve))
            vault = deploy("PolicyVault.sol/PolicyVault.json", constructor)
            transact(owner, calldata("mint(address,uint256)", vault, 5_000_000), token)
            cat = "0x" + b"infra".hex().ljust(64, "0")
            zero = "0x" + "00" * 32
            transact(owner, calldata("setCategory(bytes32,uint256,uint256,uint64,bytes32)", cat, 2_000_000, 1_000_000, 604800, zero), vault)
            transact(owner, calldata("setPayee(bytes32,address,bool,bytes32)", cat, payee, "true", zero), vault)
            journals = tmp / "payments"
            summaries = []

            def pay(invoice, amount, expected, expect_error=False):
                path = tmp / "invoice.json"
                path.write_text(json.dumps({"payment_id": invoice, "category": "infra", "payee": payee, "amount_usdc": amount}))
                cmd = [str(binary), "pay", "--request", str(path), "--vault", vault, "--from", agent, "--chain-id", "31337", "--rpc", endpoint, "--send-local", "--data-dir", str(journals)]
                run = subprocess.run(cmd, env=env, text=True, capture_output=True)
                if expect_error:
                    assert run.returncode != 0, "changed invoice must be refused"
                    summaries.append({"invoice": invoice, "changed_terms_refused": True})
                    return None
                if run.returncode:
                    raise RuntimeError(run.stderr)
                result = json.loads(run.stdout)["result"]
                assert result["status"] == expected, result
                summaries.append({"invoice": invoice, "status": result["status"], "recovered": result["recovered"], "request_id": result.get("request_id"), "decision_hash": result["decision_hash"]})
                return result

            def balance():
                return int(read("balanceOf(address)", token, payee), 16)

            first = pay("infra-invoice-001", "0.50", "paid")
            assert balance() == 500_000
            again = pay("infra-invoice-001", "0.50", "paid")
            assert again["recovered"] and balance() == 500_000
            # Lose the entire local payment journal: recover the consumed invoice
            # from decisionUsed + AgentPaid instead of treating it as a new run.
            shutil.rmtree(journals)
            recovered = pay("infra-invoice-001", "0.50", "paid")
            assert recovered["recovered"] and balance() == 500_000
            shutil.rmtree(journals)
            pay("infra-invoice-001", "0.75", "paid", expect_error=True)
            assert balance() == 500_000
            escalated = pay("infra-invoice-002", "1.50", "approval_requested")
            assert balance() == 500_000 and escalated["request_id"] == "1"
            repeated = pay("infra-invoice-002", "1.50", "approval_requested")
            assert repeated["recovered"] and int(read("requestCount()", vault), 16) == 1
            assert balance() == 500_000
            # Approval is an explicit local human-owner fixture step, not an
            # automatic action of the agent CLI.
            transact(owner, calldata("approve(uint256)", 1), vault)
            assert balance() == 2_000_000
            pay("infra-invoice-002", "1.50", "approval_requested")
            assert balance() == 2_000_000 and int(read("requestCount()", vault), 16) == 1
            summary = {"mode": "local Anvil + MockERC20; no live AI or Arc payment", "chain_id": 31337, "autonomous_paid_units": 500_000, "owner_approved_units": 1_500_000, "payee_balance_units": balance(), "approval_request_count": 1, "restart_and_lost_journal_no_double_pay": True, "steps": summaries}
            print(json.dumps(summary, indent=2))
            if args.output:
                Path(args.output).write_text(json.dumps(summary, indent=2) + "\n")
        finally:
            node.terminate()
            try:
                node.wait(timeout=5)
            except subprocess.TimeoutExpired:
                node.kill()
                node.wait()


if __name__ == "__main__":
    main()
