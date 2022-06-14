# Integration test files

The folders and files in this directory are used for the integration tests for restoring back-ups. Each folder describes a scenario that is being tested and includes the same content as a back-up archive created by the operator. If you want to manually create an archive with the content, you can change to the folder which contains the files you want to put in the archive and run `tar cvzf /tmp/your-archive-name.tar.gz -- *`.

To create an archive for the contents of the `preserve-unknown-fields` folder:

```
cd preserve-unknown-fields
tar cvzf /tmp/preserve-unknown-fields.tar.gz -- *
```
