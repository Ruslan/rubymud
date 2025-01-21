module GameEngine::ServerCommands
  def transform_from_server(string)
    string.split(/\r?\n/).map do |line|
      out_transfrom_line(line)
    end
  end

  def out_transfrom_line(line_str)
    line = GameEngine::ServerLineParsed.new(line_str)
    config.acts.each do |action|
      if line.pure_line =~ action.regexp
        # TODO: support of block?
        args = Regexp.last_match.to_a
        if action.transform
          args = args.map { |arg| action.transform.call(arg) }
        end
        commands = substitute_alias(action.command, args, 0)
        if action.button
          line.buttons += commands.split(';')
        else
          line.commands += parse(commands)
        end
        break if action.final
      end
      # TODO: Can transform lines here
    end
    line
  end
end
