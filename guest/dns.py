#!/usr/bin/env python3
# SPDX-License-Identifier: BSD-2-Clause
# One DNS question and its answer over UDP on 127.0.0.1:53: a resolver thread
# answers demo.example with 192.0.2.10 (TEST-NET-1), and the client asks it.
# The guest has no network, so this is the only resolver it can reach.
import socket
import struct
import threading

NAME, ADDR = "demo.example", "192.0.2.10"


def qname(name):
    return b"".join(bytes([len(p)]) + p.encode() for p in name.split(".")) + b"\0"


def serve(sock):
    data, peer = sock.recvfrom(512)
    qid = data[:2]
    question = data[12:]
    answer = b"\xc0\x0c" + struct.pack(">HHIH", 1, 1, 60, 4) + socket.inet_aton(ADDR)
    sock.sendto(qid + struct.pack(">HHHHH", 0x8180, 1, 1, 0, 0) + question + answer, peer)


srv = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
srv.bind(("127.0.0.1", 53))
t = threading.Thread(target=serve, args=(srv,))
t.start()
cli = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
cli.settimeout(5)
cli.sendto(struct.pack(">HHHHHH", 0x1234, 0x0100, 1, 0, 0, 0) + qname(NAME) + struct.pack(">HH", 1, 1),
           ("127.0.0.1", 53))
resp, _ = cli.recvfrom(512)
t.join()
print(f"{NAME} -> {socket.inet_ntoa(resp[-4:])}")
