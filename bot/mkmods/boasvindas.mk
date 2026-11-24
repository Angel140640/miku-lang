# ♪ Módulo boasvindas — mensagem automática quando alguém entra (precisa do módulo vars)
#
# Comandos (exigem "Gerenciar Servidor"):
#   boasvindas #canal        liga e escolhe o canal
#   msgboasvindas <texto>    muda o texto — use {user} e {server}
#   boasvindas_off           desliga
# Usa a intent privilegiada "Server Members": ative em Developer Portal → Bot → Server Members Intent.

intents members

on member_join(membro):
    gid = membro.guild_id
    canal = var_get(f"bv:canal:{gid}", None)
    if canal is None:
        return
    texto = var_get(f"bv:msg:{gid}", "Bem-vindo(a) {user} ao **{server}**! ♪")
    texto = texto.replace("{user}", membro.mention).replace("{server}", membro.guild.name)
    bot.send(canal, texto)

command boasvindas(canal: channel) requires manage_guild:
    if not ctx.guild_id:
        reply "Esse comando só funciona em servidores."
        return
    var_set(f"bv:canal:{ctx.guild_id}", canal.id)
    reply f"✅ Boas-vindas ligadas em {canal.mention}."

command msgboasvindas(*, texto) requires manage_guild:
    if not ctx.guild_id:
        reply "Esse comando só funciona em servidores."
        return
    var_set(f"bv:msg:{ctx.guild_id}", texto)
    reply "✅ Mensagem atualizada."

command boasvindas_off requires manage_guild:
    if not ctx.guild_id:
        reply "Esse comando só funciona em servidores."
        return
    var_del(f"bv:canal:{ctx.guild_id}")
    reply "✅ Boas-vindas desligadas."
