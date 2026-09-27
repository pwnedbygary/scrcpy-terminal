# Started by name through `app_process / com.genymobile.scrcpy.Server`, so R8
# cannot see any reference to it from the app.
-keep class com.genymobile.scrcpy.** { *; }
-keep class android.content.IOnPrimaryClipChangedListener { *; }
-keep class android.content.IOnPrimaryClipChangedListener$* { *; }
-keep class android.view.IDisplayWindowListener { *; }
-keep class android.view.IDisplayWindowListener$* { *; }
-keep class android.content.IContentProvider { *; }
