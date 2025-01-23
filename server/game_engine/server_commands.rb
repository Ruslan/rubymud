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
        # Retrive arguments and prepare it
        args = Regexp.last_match.to_a
        if action.transform
          args = args.map { |arg| action.transform.call(arg) }
        end

        commands = ''
        if action.block
          commands = if action.block.parameters[0] && action.block.parameters[0][1] == :line
            vm.instance_exec(line, *args, &action.block)
          else
            vm.instance_exec(*args, &action.block)
          end
        elsif action.command
          commands = substitute_alias(action.command, args, 0)
        end

        if action.button
          line.buttons += commands.split(';')
        else
          line.commands += parse(commands)
        end

        if action.window
          line.window = action.window
        end
        break if action.final
      end

      # TODO: Can transform lines here
    end
    line
  end
end
