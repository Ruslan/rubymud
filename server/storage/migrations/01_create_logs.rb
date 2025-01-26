Sequel.migration do
  change do
    create_table(:logs) do
      primary_key :id, type: :integer
      String :line
      String :pure_line
      String :commands
      String :buttons
      String :window
      DateTime :created_at, default: Sequel::CURRENT_TIMESTAMP

      index :window
      index :created_at
    end
  end
end
