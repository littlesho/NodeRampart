#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Finite multi-interface/IPv6 AF_PACKET lab, only in an authorized disposable VM.

Creates two namespaces with two veth pairs. All routes and UDP exchanges remain
inside those new namespaces; no parent route, firewall or service is changed.
"""
import argparse
import importlib.util
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import tempfile
import time

spec = importlib.util.spec_from_file_location("netns_guards", Path(__file__).with_name("test-netns.py"))
guards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guards)

SERVER = r'''
import pathlib,selectors,socket,sys,time
s=selectors.DefaultSelector()
for family,address in ((socket.AF_INET,('192.0.2.2',39801)),(socket.AF_INET6,('2001:db8:3::2',39801))):
 sock=socket.socket(family,socket.SOCK_DGRAM); sock.bind(address); s.register(sock,selectors.EVENT_READ)
pathlib.Path(sys.argv[1]).touch(mode=0o600)
end=time.monotonic()+15
count=0
while count<10 and time.monotonic()<end:
 for key,_ in s.select(timeout=.1):
  data,address=key.fileobj.recvfrom(32)
  if data!=b'synthetic': raise SystemExit(2)
  key.fileobj.sendto(data,address); count+=1
raise SystemExit(0 if count==10 else 3)
'''

def run(binary):
    deadline = time.monotonic() + 22
    ip = shutil.which("ip")
    if not ip:
        raise guards.LabFailure("iproute2 required")
    namespaces, processes = [], []

    def remaining():
        seconds = deadline - time.monotonic()
        if seconds <= 0:
            raise guards.LabFailure("multi-interface fixture deadline exceeded")
        return seconds

    def command(args):
        result = subprocess.run([ip, *args], stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, timeout=min(3, remaining()))
        if result.returncode:
            raise guards.LabFailure("isolated multi-interface setup failed")

    def interrupted(*_):
        raise guards.LabFailure("fixture interrupted")

    previous = {sig: signal.signal(sig, interrupted) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        with tempfile.TemporaryDirectory(prefix="noderampart-multi-") as directory:
            suffix = secrets.token_hex(12)
            client, server = "nr-multi-" + suffix + "-c", "nr-multi-" + suffix + "-s"
            for namespace in (client, server):
                command(["netns", "add", namespace]); namespaces.append(namespace)
                command(["-n", namespace, "link", "set", "lo", "up"])
            for name in ("nr4", "nr6"):
                command(["-n", client, "link", "add", name, "type", "veth", "peer", "name", name, "netns", server])
                for namespace in (client, server):
                    command(["-n", namespace, "link", "set", name, "up"])
            for namespace, last in ((client, "1"), (server, "2")):
                command(["-n", namespace, "-4", "address", "add", "192.0.2." + last + "/30", "dev", "nr4"])
                command(["-n", namespace, "-6", "address", "add", "2001:db8:3::" + last + "/126", "nodad", "dev", "nr6"])
            command(["-n", client, "-4", "route", "add", "default", "via", "192.0.2.2", "dev", "nr4"])
            command(["-n", client, "-6", "route", "add", "default", "via", "2001:db8:3::2", "dev", "nr6"])
            ready = Path(directory, "ready")
            peer = subprocess.Popen([ip, "netns", "exec", server, "/usr/bin/python3", "-c", SERVER, str(ready)])
            processes.append(peer)
            while not ready.exists():
                remaining()
                if peer.poll() is not None:
                    raise guards.LabFailure("isolated peer failed to start")
                time.sleep(.025)
            env = {**os.environ, "NODERAMPART_MULTI_LAB": "authorized-disposable-vm-v1", "NODERAMPART_MULTI_NS": client}
            capture = subprocess.Popen([ip, "netns", "exec", client, str(binary), "-test.run=^TestNetNSMultipleInterfacesIPv6AndRouteChanges$", "-test.v", "-test.timeout=16s"], env=env)
            processes.append(capture)
            if capture.wait(timeout=remaining()) or peer.wait(timeout=remaining()):
                raise guards.LabFailure("multi-interface capture assertions failed")
    finally:
        for sig in previous:
            signal.signal(sig, signal.SIG_IGN)
        failed = False
        for process in processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=.25)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait(timeout=1)
        for namespace in reversed(namespaces):
            try:
                result = subprocess.run([ip, "netns", "delete", namespace], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=2)
                failed |= result.returncode != 0
            except (OSError, subprocess.TimeoutExpired):
                failed = True
        for sig, handler in previous.items():
            signal.signal(sig, handler)
        if failed:
            raise guards.LabFailure("fixture cleanup failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--authorized-disposable-lab", action="store_true", required=True)
    parser.add_argument("--test-binary", type=Path, required=True)
    args = parser.parse_args()
    guards.authorize_lab(args.authorized_disposable_lab, time.monotonic() + 3)
    binary = args.test_binary
    if not binary.is_absolute() or binary.is_symlink() or not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error("test binary must be an absolute regular executable")
    run(binary)


if __name__ == "__main__":
    main()
