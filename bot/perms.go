package bot

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// permBits: nomes (estilo discord.py) → bits de permissão do Discord.
var permBits = map[string]uint64{
	"create_invite": 1 << 0, "kick_members": 1 << 1, "ban_members": 1 << 2, "administrator": 1 << 3,
	"manage_channels": 1 << 4, "manage_guild": 1 << 5, "add_reactions": 1 << 6, "view_audit_log": 1 << 7,
	"view_channel": 1 << 10, "send_messages": 1 << 11, "manage_messages": 1 << 13, "embed_links": 1 << 14,
	"attach_files": 1 << 15, "read_message_history": 1 << 16, "mention_everyone": 1 << 17,
	"use_external_emojis": 1 << 18, "connect": 1 << 20, "speak": 1 << 21, "mute_members": 1 << 22,
	"deafen_members": 1 << 23, "move_members": 1 << 24, "change_nickname": 1 << 26,
	"manage_nicknames": 1 << 27, "manage_roles": 1 << 28, "manage_webhooks": 1 << 29,
	"manage_emojis": 1 << 30, "moderate_members": 1 << 40,
}

func permNames() string {
	n := make([]string, 0, len(permBits))
	for k := range permBits {
		n = append(n, k)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

// parsePerms converte nomes em bits (erro se algum nome não existir).
func parsePerms(names []string) (uint64, error) {
	var bits uint64
	for _, n := range names {
		bit, ok := permBits[strings.ToLower(n)]
		if !ok {
			return 0, fmt.Errorf("permissão '%s' desconhecida. Use: %s", n, permNames())
		}
		bits |= bit
	}
	return bits, nil
}

const allPerms = ^uint64(0)

// guildPerms calcula as permissões (no nível do servidor) de um membro usando o cache do servidor.
// Dono e administradores têm tudo. Retorna ok=false se o servidor não está no cache.
func (b *Bot) guildPerms(guildID, userID string, roleIDs []string) (uint64, bool) {
	g := b.cachedGuild(guildID)
	if g == nil {
		return 0, false
	}
	if str(g, "owner_id") == userID {
		return allPerms, true
	}
	have := map[string]bool{guildID: true} // @everyone tem o id do servidor
	for _, r := range roleIDs {
		have[r] = true
	}
	var bits uint64
	for _, r := range sl(g, "roles") {
		rm, _ := r.(map[string]any)
		if have[str(rm, "id")] {
			v, _ := strconv.ParseUint(str(rm, "permissions"), 10, 64)
			bits |= v
		}
	}
	if bits&permBits["administrator"] != 0 {
		return allPerms, true
	}
	return bits, true
}

func memberRoleIDs(m map[string]any) []string {
	var out []string
	for _, r := range sl(m, "roles") {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// missingPerms devolve os nomes das permissões que faltam.
func missingPerms(have uint64, names []string) []string {
	var miss []string
	for _, n := range names {
		if have&permBits[strings.ToLower(n)] == 0 {
			miss = append(miss, strings.ToLower(n))
		}
	}
	return miss
}
