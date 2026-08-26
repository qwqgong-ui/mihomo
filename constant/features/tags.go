package features

func Tags() (tags []string) {
	if CMFA {
		tags = append(tags, "cmfa")
	}
	if WithLowMemory {
		tags = append(tags, "with_low_memory")
	}
	if NoFakeTCP {
		tags = append(tags, "no_fake_tcp")
	}
	if NoTailscale {
		tags = append(tags, "no_tailscale")
	}
	if NoZeroTier {
		tags = append(tags, "no_zerotier")
	}
	if NoWireGuard {
		tags = append(tags, "no_wireguard")
	}
	if NoOpenVPN {
		tags = append(tags, "no_openvpn")
	}
	if NoMieru {
		tags = append(tags, "no_mieru")
	}
	if NoSudoku {
		tags = append(tags, "no_sudoku")
	}
	if WithGVisor {
		tags = append(tags, "with_gvisor")
	}
	return
}
