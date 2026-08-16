#!/usr/bin/env python3
"""Parse & disassemble Python 2.3 .pyc files (magic 62011) with Python 3.

QQTang client UI scripts are compiled with Python 2.3. This tool extracts
code objects, constants (GBK strings), names, and a linear disassembly so we
can recover UI layout (control positions, image paths) faithfully.
"""
import struct
import sys

# ---------------- marshal (Python 2.3 format) ----------------

class Code:
    def __init__(self):
        self.argcount = 0
        self.nlocals = 0
        self.stacksize = 0
        self.flags = 0
        self.code = b""
        self.consts = ()
        self.names = ()
        self.varnames = ()
        self.freevars = ()
        self.cellvars = ()
        self.filename = ""
        self.name = ""
        self.firstlineno = 0
        self.lnotab = b""

class Reader:
    def __init__(self, data):
        self.data = data
        self.pos = 0
        self.interned = []

    def u8(self):
        v = self.data[self.pos]
        self.pos += 1
        return v

    def bytes(self, n):
        v = self.data[self.pos:self.pos + n]
        self.pos += n
        return v

    def i32(self):
        return struct.unpack("<i", self.bytes(4))[0]

    def load(self):
        t = chr(self.u8())
        if t == "N":
            return None
        if t == "0":
            return None
        if t == "F":
            return False
        if t == "T":
            return True
        if t == "i":
            return self.i32()
        if t == "I":
            return struct.unpack("<q", self.bytes(8))[0]
        if t == "f":
            n = self.u8()
            return float(self.bytes(n).decode("ascii"))
        if t == "l":
            n = self.i32()
            sign = 1
            if n < 0:
                sign = -1
                n = -n
            val = 0
            for i in range(n):
                d = struct.unpack("<H", self.bytes(2))[0]
                val |= d << (15 * i)
            return sign * val
        if t == "s":
            n = self.i32()
            return self.bytes(n)
        if t == "t":
            n = self.i32()
            s = self.bytes(n)
            self.interned.append(s)
            return s
        if t == "R":
            return self.interned[self.i32()]
        if t == "u":
            n = self.i32()
            return self.bytes(n).decode("utf-8", "replace")
        if t == "(":
            n = self.i32()
            return tuple(self.load() for _ in range(n))
        if t == "[":
            n = self.i32()
            return [self.load() for _ in range(n)]
        if t == "{":
            d = {}
            while True:
                k = self.load()
                if k is None:
                    break
                d[bytes_key(k)] = self.load()
            return d
        if t == "c":
            c = Code()
            c.argcount = self.i32()
            c.nlocals = self.i32()
            c.stacksize = self.i32()
            c.flags = self.i32()
            c.code = self.load()
            c.consts = self.load()
            c.names = self.load()
            c.varnames = self.load()
            c.freevars = self.load()
            c.cellvars = self.load()
            c.filename = self.load()
            c.name = self.load()
            c.firstlineno = self.i32()
            c.lnotab = self.load()
            return c
        raise ValueError(f"unknown marshal type {t!r} at {self.pos-1}")

def bytes_key(k):
    return k.decode("gbk", "replace") if isinstance(k, bytes) else k

# ---------------- Python 2.3 opcode table ----------------

OPNAMES = {
    0: "STOP_CODE", 1: "POP_TOP", 2: "ROT_TWO", 3: "ROT_THREE", 4: "DUP_TOP",
    5: "ROT_FOUR", 10: "UNARY_POSITIVE", 11: "UNARY_NEGATIVE", 12: "UNARY_NOT",
    13: "UNARY_CONVERT", 15: "UNARY_INVERT", 19: "BINARY_POWER",
    20: "BINARY_MULTIPLY", 21: "BINARY_DIVIDE", 22: "BINARY_MODULO",
    23: "BINARY_ADD", 24: "BINARY_SUBTRACT", 25: "BINARY_SUBSCR",
    26: "BINARY_FLOOR_DIVIDE", 27: "BINARY_TRUE_DIVIDE",
    28: "INPLACE_FLOOR_DIVIDE", 29: "INPLACE_TRUE_DIVIDE",
    30: "SLICE+0", 31: "SLICE+1", 32: "SLICE+2", 33: "SLICE+3",
    40: "STORE_SLICE+0", 41: "STORE_SLICE+1", 42: "STORE_SLICE+2", 43: "STORE_SLICE+3",
    50: "DELETE_SLICE+0", 51: "DELETE_SLICE+1", 52: "DELETE_SLICE+2", 53: "DELETE_SLICE+3",
    55: "INPLACE_ADD", 56: "INPLACE_SUBTRACT", 57: "INPLACE_MULTIPLY",
    58: "INPLACE_DIVIDE", 59: "INPLACE_MODULO", 60: "STORE_SUBSCR",
    61: "DELETE_SUBSCR", 62: "BINARY_LSHIFT", 63: "BINARY_RSHIFT",
    64: "BINARY_AND", 65: "BINARY_XOR", 66: "BINARY_OR", 67: "INPLACE_POWER",
    68: "GET_ITER", 70: "PRINT_EXPR", 71: "PRINT_ITEM", 72: "PRINT_NEWLINE",
    73: "PRINT_ITEM_TO", 74: "PRINT_NEWLINE_TO", 75: "INPLACE_LSHIFT",
    76: "INPLACE_RSHIFT", 77: "INPLACE_AND", 78: "INPLACE_XOR", 79: "INPLACE_OR",
    80: "BREAK_LOOP", 82: "LOAD_LOCALS", 83: "RETURN_VALUE", 84: "IMPORT_STAR",
    85: "EXEC_STMT", 86: "YIELD_VALUE", 87: "POP_BLOCK", 88: "END_FINALLY",
    89: "BUILD_CLASS",
    90: "STORE_NAME", 91: "DELETE_NAME", 92: "UNPACK_SEQUENCE", 93: "FOR_ITER",
    95: "STORE_ATTR", 96: "DELETE_ATTR", 97: "STORE_GLOBAL", 98: "DELETE_GLOBAL",
    99: "DUP_TOPX", 100: "LOAD_CONST", 101: "LOAD_NAME", 102: "BUILD_TUPLE",
    103: "BUILD_LIST", 104: "BUILD_MAP", 105: "LOAD_ATTR", 106: "COMPARE_OP",
    107: "IMPORT_NAME", 108: "IMPORT_FROM", 110: "JUMP_FORWARD",
    111: "JUMP_IF_FALSE", 112: "JUMP_IF_TRUE", 113: "JUMP_ABSOLUTE",
    116: "LOAD_GLOBAL", 119: "CONTINUE_LOOP", 120: "SETUP_LOOP",
    121: "SETUP_EXCEPT", 122: "SETUP_FINALLY", 124: "LOAD_FAST",
    125: "STORE_FAST", 126: "DELETE_FAST", 130: "RAISE_VARARGS",
    131: "CALL_FUNCTION", 132: "MAKE_FUNCTION", 133: "BUILD_SLICE",
    134: "MAKE_CLOSURE", 135: "LOAD_CLOSURE", 136: "LOAD_DEREF",
    137: "STORE_DEREF", 140: "CALL_FUNCTION_VAR", 141: "CALL_FUNCTION_KW",
    142: "CALL_FUNCTION_VAR_KW", 143: "EXTENDED_ARG",
}
CMP_OPS = ["<", "<=", "==", "!=", ">", ">=", "in", "not in", "is", "is not",
           "exception match", "BAD"]

def fmt_const(v):
    if isinstance(v, bytes):
        try:
            return repr(v.decode("gbk"))
        except Exception:
            return repr(v)
    if isinstance(v, Code):
        return f"<code {v.name and dec(v.name)}>"
    if isinstance(v, tuple):
        return "(" + ", ".join(fmt_const(x) for x in v) + ")"
    return repr(v)

def dec(v):
    return v.decode("gbk", "replace") if isinstance(v, bytes) else str(v)

def disassemble(code, indent=0, out=None):
    pad = "  " * indent
    hdr = f"{pad}== code {dec(code.name)}  args={code.argcount} @line {code.firstlineno}"
    out.append(hdr)
    bc = code.code
    i = 0
    ext = 0
    while i < len(bc):
        op = bc[i]
        name = OPNAMES.get(op, f"OP_{op}")
        if op >= 90:
            arg = bc[i + 1] | (bc[i + 2] << 8) | ext
            ext = 0
            if name == "EXTENDED_ARG":
                ext = arg << 16
                i += 3
                continue
            detail = ""
            try:
                if name == "LOAD_CONST":
                    detail = fmt_const(code.consts[arg])
                elif name in ("LOAD_NAME", "STORE_NAME", "DELETE_NAME",
                              "LOAD_ATTR", "STORE_ATTR", "DELETE_ATTR",
                              "LOAD_GLOBAL", "STORE_GLOBAL", "IMPORT_NAME",
                              "IMPORT_FROM"):
                    detail = dec(code.names[arg])
                elif name in ("LOAD_FAST", "STORE_FAST", "DELETE_FAST"):
                    detail = dec(code.varnames[arg])
                elif name in ("LOAD_DEREF", "STORE_DEREF", "LOAD_CLOSURE"):
                    cells = tuple(code.cellvars) + tuple(code.freevars)
                    detail = dec(cells[arg])
                elif name == "COMPARE_OP":
                    detail = CMP_OPS[arg]
                else:
                    detail = str(arg)
            except Exception:
                detail = f"?{arg}"
            out.append(f"{pad}{i:5d} {name:<22s} {detail}")
            i += 3
        else:
            out.append(f"{pad}{i:5d} {name}")
            i += 1
    for c in code.consts:
        if isinstance(c, Code):
            out.append("")
            disassemble(c, indent + 1, out)

def main():
    path = sys.argv[1]
    with open(path, "rb") as f:
        data = f.read()
    magic = struct.unpack("<H", data[:2])[0]
    r = Reader(data[8:])
    top = r.load()
    out = [f"# {path}  magic={magic}"]
    disassemble(top, 0, out)
    print("\n".join(out))

if __name__ == "__main__":
    main()
