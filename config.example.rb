# Sample config

class Config
  extend Configurable

  # Hotkeys section, hotkey format there https://github.com/jaywcjlove/hotkeys-js
  key 'f4', 'взя тру;взя все тру;брос тру'
  key 'pageup', 'север'
  key 'pagedown', 'юг'
  key 'end', 'восток'
  key 'home', 'запад'

  # Variables section, use them with $var in any commands
  var 'сумка', 'сумк'

  # Aliases section.
  #
  # @param alias_name [String] The alias or shortcut that triggers the corresponding command.
  #   This is typically a shortened version of the full command that you want to map to a longer action.
  #   Example: `'уу'` will trigger the command `'у %1;пари'`.
  #
  # @param command [String] The full command that will be triggered when the alias is used.
  #   It may include placeholders like `%1`, `%2`, etc., which are replaced with the arguments passed to the alias.
  #   Example: `'у %1;пари'` will use the value of `%1` (the argument) in place of the placeholder.
  #
  # @example
  #   aliass 'уу', 'у %1;пари'
  #   # Triggers the command "у [argument];пари" when the alias "уу" is used.
  #   # The argument will replace the "%1" placeholder in the command.
  #
  #   aliass 'сняя', 'сня %1;пол %1 $сумка'
  #   # Triggers the command "сня [argument];пол [argument] $сумка" when the alias "сняя" is used.
  #   # The argument will replace "%1" in both parts of the command.
  #
  #   aliass 'надее', 'взя %1 $сумка;наде %1'
  #   # Triggers the command "взя [argument] $сумка;наде [argument]" when the alias "надее" is used.
  #   # The argument will replace "%1" in both parts of the command.
  aliass 'уу', 'у %1;пари'
  aliass 'сняя', 'сня %1;пол %1 $сумка'
  aliass 'надее', 'взя %1 $сумка;наде %1'

  # Triggers section.
  #
  # @param regexp [Regexp] The regular expression pattern that should be matched in the game message.
  #   If the pattern matches part of a message, the corresponding action will be triggered.
  #   Example: /^(Крыса) прибежала/ matches messages starting with "Крыса прибежала".
  #
  # @param response [String] The response string to be sent or logged when the pattern matches.
  #   It can include capture groups from the regular expression (e.g., %1) which will be replaced
  #   by the corresponding matched parts from the message. Example: 'привет %1' sends a greeting
  #   with the captured group from the pattern.
  #
  # @param button [Boolean] (optional) If set to true, the action will include a button in the UI
  #   that can be pressed to trigger the action. This is useful for situational triggers where you
  #   need to decide before performing the action. When `button` is false, the command runs immediately.
  #   Example: If `button` is true, a button appears, and to activate the command, you need to press the button.
  #
  # @param transform [Proc] (optional) A lambda or proc that is applied to the captured value
  #   before it is used in the action. This is useful for modifying or formatting the matched string.
  #   Example: `-> { _1.to_s[0..-2] }` removes the last character from the matched string.
  act(/^(Крыса) прибежала/, 'привет %1')
  act(/^Маленький (паучок) ползет по паутине./, 'привет %1', button: true)
  act(/^Вы ударили (.+) ногой, ломая \S+ кости!$/, 'пнуть %1', button: true, transform: -> { _1.to_s[0..-2] })
  act(/R.I.P.$/, 'взя все *.тру', button: true)
  act(/R.I.P.$/, 'взя моне *.тру', button: true)
  act(/^Угрюмый человек идет по проходу./, 'у клад', button: true)
end
