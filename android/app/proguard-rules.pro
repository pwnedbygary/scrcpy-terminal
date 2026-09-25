# kotlinx.serialization ships its own R8 rules for generated serializers.
# The vendored scrcpy server keeps itself via :scrcpy-server consumer rules.

# Started by name through `app_process` in the helper activation command.
-keep class io.github.pwnedbygary.scterm.helper.HelperMain {
    public static void main(java.lang.String[]);
}

# Keep line numbers for readable crash reports; hide the source file names.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile
